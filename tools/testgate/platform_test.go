package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPlatformNameInAStringIsAHitWithFileLineAndName(t *testing.T) {
	src := "package cli\n\nimport \"os/exec\"\n\nfunc f() {\n\texec.Command(\"wslpath\", \"-u\", \"x\")\n}\n"
	hits, err := scanPlatform("internal/cli/x.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := []platformHit{{"internal/cli/x.go", 6, "wslpath"}}
	if !reflect.DeepEqual(hits, want) {
		t.Fatalf("hits %v, want %v", hits, want)
	}
	if s := hits[0].String(); !strings.Contains(s, "internal/cli/x.go:6") || !strings.Contains(s, "wslpath") {
		t.Fatalf("%q names no file, line or name", s)
	}
}

func TestEveryListedNameInAStringIsAHit(t *testing.T) {
	for _, n := range platformNames {
		if n.selector {
			continue
		}
		src := "package a\n\nvar s = `C:\\x\\" + n.name + " -flag`\n"
		hits, err := scanPlatform("a.go", []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 1 || hits[0].name != n.name {
			t.Errorf("%s: hits %v", n.name, hits)
		}
	}
}

func TestRuntimeGOOSIsAHitUnderAnyImportName(t *testing.T) {
	for _, src := range []string{
		"package a\n\nimport \"runtime\"\n\nvar s = runtime.GOOS\n",
		"package a\n\nimport rt \"runtime\"\n\nvar s = rt.GOOS\n",
	} {
		hits, err := scanPlatform("a.go", []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		if want := []platformHit{{"a.go", 5, "runtime.GOOS"}}; !reflect.DeepEqual(hits, want) {
			t.Errorf("%q: hits %v, want %v", src, hits, want)
		}
	}
}

func TestNotAHitInCommentsOtherWordsOrOtherSelectors(t *testing.T) {
	src := "package a\n\n// starts hq through wsl.exe; runtime.GOOS\nimport \"runtime\"\n\n" +
		"type x struct{ GOOS string }\n\n" +
		"var (\n\tarch = runtime.GOARCH\n\tv = x{}.GOOS\n\tmsg = \"hq sits in the system tray\"\n\tstub = \"mysbx.executable\"\n\tsbx = \"sbx\"\n)\n"
	hits, err := scanPlatform("a.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("hits %v", hits)
	}
}

func TestCheckPlatformLeavesOutTestsThePlatformPackagesAndHidden(t *testing.T) {
	root := t.TempDir()
	code := "package a\n\nvar s = \"wslpath\"\n"
	write(t, root, "internal/cli/x.go", code)
	write(t, root, "internal/cli/x_test.go", code)
	write(t, root, "internal/cli/x_integration_test.go", code)
	write(t, root, "internal/platform/p.go", code)
	write(t, root, "internal/platform/deep/p.go", code)
	write(t, root, "internal/testutil/t.go", code)
	write(t, root, "internal/platformer/p.go", code)
	write(t, root, ".claude/worktrees/other/internal/cli/x.go", code)
	write(t, root, "internal/cli/testdata/x.go", code)
	write(t, root, "cmd/hq/notes.txt", "wslpath\n")
	hits, err := checkPlatform(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []platformHit{
		{filepath.Join("internal", "cli", "x.go"), 3, "wslpath"},
		{filepath.Join("internal", "platformer", "p.go"), 3, "wslpath"},
	}
	if !reflect.DeepEqual(hits, want) {
		t.Fatalf("hits %v, want %v", hits, want)
	}
}

func TestEveryExemptionSaysWhy(t *testing.T) {
	for _, e := range platformExempt {
		if strings.TrimSpace(e.why) == "" {
			t.Errorf("exemption %s has no reason", e.dir)
		}
	}
}

func TestCheckPlatformFailsOnCodeThatDoesNotParse(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a/x.go", "package a\nfunc {\n")
	if _, err := checkPlatform(root); err == nil {
		t.Fatal("no error for a file that does not parse")
	}
}

func TestNameAfterAnEscapeOrInAnotherCaseIsAHit(t *testing.T) {
	src := "package a\n\nvar (\n\tx = \"\\tcmd.exe /c\"\n\ty = \"\\nwslpath\"\n\tz = \"C:\\\\WINDOWS\\\\CMD.EXE\"\n)\n"
	hits, err := scanPlatform("a.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := []platformHit{{"a.go", 4, "cmd.exe"}, {"a.go", 5, "wslpath"}, {"a.go", 6, "cmd.exe"}}
	if !reflect.DeepEqual(hits, want) {
		t.Fatalf("hits %v, want %v", hits, want)
	}
}

func TestGOOSOfADotImportedRuntimeIsAHit(t *testing.T) {
	src := "package a\n\nimport . \"runtime\"\n\nvar s = GOOS\n"
	hits, err := scanPlatform("a.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if want := []platformHit{{"a.go", 5, "runtime.GOOS"}}; !reflect.DeepEqual(hits, want) {
		t.Fatalf("hits %v, want %v", hits, want)
	}
}
