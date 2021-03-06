package main

import (
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"golang.org/x/tools/go/packages"
)

func goMacBind(gobind string, pkgs []*packages.Package, archs []string) error {
	// Run gobind to generate the bindings
	fmt.Println("\tHere", 0)
	cmd := exec.Command(
		gobind,
		"-lang=go,objc",
		"-outdir="+tmpdir,
	)
	fmt.Println("\tHere", 1)
	cmd.Env = append(cmd.Env, "GOOS=darwin")
	cmd.Env = append(cmd.Env, "CGO_ENABLED=1")
	tags := append(buildTags, "ios")
	cmd.Args = append(cmd.Args, "-tags="+strings.Join(tags, ","))
	if bindPrefix != "" {
		cmd.Args = append(cmd.Args, "-prefix="+bindPrefix)
	}
	for _, p := range pkgs {
		cmd.Args = append(cmd.Args, p.PkgPath)
	}
	fmt.Println("\tHere", 2)
	if err := runCmd(cmd); err != nil {
		fmt.Println("\tHere", "Error")
		return err
	}

	fmt.Println("\tHere", 3)
	srcDir := filepath.Join(tmpdir, "src", "gobind")

	name := pkgs[0].Name
	title := strings.Title(name)

	if buildO != "" && !strings.HasSuffix(buildO, ".framework") {
		return fmt.Errorf("static framework name %q missing .framework suffix", buildO)
	}
	if buildO == "" {
		buildO = title + ".framework"
	}

	fileBases := make([]string, len(pkgs)+1)
	for i, pkg := range pkgs {
		fileBases[i] = bindPrefix + strings.Title(pkg.Name)
	}
	fileBases[len(fileBases)-1] = "Universe"
	fmt.Println("\tHere", 4)

	cmd = exec.Command("xcrun", "lipo", "-create")

	for _, arch := range archs {
		fmt.Println("\tArchitecture: ", arch)
		if err := writeGoMod("darwin", arch); err != nil {
			return err
		}

		env := macosxEnv[arch]
		// Add the generated packages to GOPATH for reverse bindings.
		gopath := fmt.Sprintf("GOPATH=%s%c%s", tmpdir, filepath.ListSeparator, goEnv("GOPATH"))
		env = append(env, gopath)
		path, err := goMacBindArchive(name, env, filepath.Join(tmpdir, "src"))
		fmt.Println("Created static archive: ", path, err)
		if err != nil {
			return fmt.Errorf("darwin-%s: %v", arch, err)
		}
		cmd.Args = append(cmd.Args, "-arch", archClang(arch), path)
		runCmd(exec.Command("cp", path, "/Users/Dev/Downloads/"+name+"-"+arch+".a"))
	}

	// Build static framework output directory.
	if err := removeAll(buildO); err != nil {
		return err
	}
	headers := buildO + "/Versions/A/Headers"
	if err := mkdir(headers); err != nil {
		return err
	}
	if err := symlink("A", buildO+"/Versions/Current"); err != nil {
		return err
	}
	if err := symlink("Versions/Current/Headers", buildO+"/Headers"); err != nil {
		return err
	}
	if err := symlink("Versions/Current/"+title, buildO+"/"+title); err != nil {
		return err
	}

	cmd.Args = append(cmd.Args, "-o", buildO+"/Versions/A/"+title)
	if err := runCmd(cmd); err != nil {
		return err
	}

	// Copy header file next to output archive.
	headerFiles := make([]string, len(fileBases))
	if len(fileBases) == 1 {
		headerFiles[0] = title + ".h"
		err := copyFile(
			headers+"/"+title+".h",
			srcDir+"/"+bindPrefix+title+".objc.h",
		)
		if err != nil {
			return err
		}
	} else {
		for i, fileBase := range fileBases {
			headerFiles[i] = fileBase + ".objc.h"
			err := copyFile(
				headers+"/"+fileBase+".objc.h",
				srcDir+"/"+fileBase+".objc.h")
			if err != nil {
				return err
			}
		}
		err := copyFile(
			headers+"/ref.h",
			srcDir+"/ref.h")
		if err != nil {
			return err
		}
		headerFiles = append(headerFiles, title+".h")
		err = writeFile(headers+"/"+title+".h", func(w io.Writer) error {
			return macBindHeaderTmpl.Execute(w, map[string]interface{}{
				"pkgs": pkgs, "title": title, "bases": fileBases,
			})
		})
		if err != nil {
			return err
		}
	}

	resources := buildO + "/Versions/A/Resources"
	if err := mkdir(resources); err != nil {
		return err
	}
	if err := symlink("Versions/Current/Resources", buildO+"/Resources"); err != nil {
		return err
	}
	err := writeFile(buildO+"/Resources/Info.plist", func(w io.Writer) error {
		_, err := w.Write([]byte(macBindInfoPlist))
		return err
	})
	if err != nil {
		return err
	}

	var mmVals = struct {
		Module  string
		Headers []string
	}{
		Module:  title,
		Headers: headerFiles,
	}
	err = writeFile(buildO+"/Versions/A/Modules/module.modulemap", func(w io.Writer) error {
		return macModuleMapTmpl.Execute(w, mmVals)
	})
	if err != nil {
		return err
	}
	return symlink("Versions/Current/Modules", buildO+"/Modules")
}

const macBindInfoPlist = `<?xml version="1.0" encoding="UTF-8"?>
    <!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
    <plist version="1.0">
      <dict>
      </dict>
    </plist>
`

var macModuleMapTmpl = template.Must(template.New("macmmap").Parse(`framework module "{{.Module}}" {
	header "ref.h"
{{range .Headers}}    header "{{.}}"
{{end}}
    export *
}`))

func goMacBindArchive(name string, env []string, gosrc string) (string, error) {
	arch := getenv(env, "GOARCH")
	archive := filepath.Join(tmpdir, name+"-"+arch+".a")
	fmt.Println("====> Creating static library for: ", name, arch)
	fmt.Println("====> \t", gosrc, env, archive)
	err := goBuildAt(gosrc, "./gobind", env, "-buildmode=c-archive", "-o", archive)
	if err != nil {
		return "", err
	}
	return archive, nil
}

var macBindHeaderTmpl = template.Must(template.New("mac.h").Parse(`
// Objective-C API for talking to the following Go packages
//
{{range .pkgs}}//	{{.PkgPath}}
{{end}}//
// File is generated by gomobile bind. Do not edit.
#ifndef __{{.title}}_FRAMEWORK_H__
#define __{{.title}}_FRAMEWORK_H__

{{range .bases}}#include "{{.}}.objc.h"
{{end}}
#endif
`))
