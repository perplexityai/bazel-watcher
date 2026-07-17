package simple

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bazelbuild/bazel-watcher/internal/e2e"
)

const secondaryBuild = `
sh_library(
	name = "lib",
	data = ["lib.sh"],
	visibility = ["//visibility:public"],
)
`

const secondaryLib = `
function say_hello {
	printf "hello!"
}
`

const secondaryLibAlt = `
function say_hello {
	printf "hello2!"
}
`

const mainFiles = `
-- BUILD.bazel --
sh_binary(
	name = "test",
	srcs = ["test.sh"],
	deps = [
		"@secondary//:lib",
	],
)
-- test.sh --
#!/bin/bash
source ../secondary/lib.sh
say_hello
-- WORKSPACE --
local_repository(
    name = "secondary",
    path = "../secondary",
)
`

var (
	secondaryWd  string
	secondaryWd2 string
)

func createSecondaryWorkspace(wd string) error {
	if err := os.Mkdir(wd, 0777); err != nil {
		return fmt.Errorf("os.Mkdir(%q): %w", wd, err)
	}
	for file, contents := range map[string]string{
		".bazelversion": "6.5.0",
		"BUILD.bazel":   secondaryBuild,
		"lib.sh":        secondaryLib,
		"WORKSPACE":     "",
		"MODULE.bazel":  "",
	} {
		if err := ioutil.WriteFile(filepath.Join(wd, file), []byte(contents), 0777); err != nil {
			return fmt.Errorf("failed to write file %q: %w", file, err)
		}
	}
	return nil
}

func TestMain(m *testing.M) {
	e2e.TestMain(m, e2e.Args{
		Main: mainFiles,
		SetUp: func() error {
			// Create two secondary workspaces in sibling folders of the main workspace.
			secondaryWd, _ = filepath.Abs(filepath.Join("..", "secondary"))
			secondaryWd2, _ = filepath.Abs(filepath.Join("..", "secondary-2"))

			for _, wd := range []string{secondaryWd, secondaryWd2} {
				if err := createSecondaryWorkspace(wd); err != nil {
					return err
				}
			}
			return nil
		},
	})
}

func TestRunWithModifiedFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skipf("--override_repository is not implemented in windows")
	}

	ibazel := e2e.SetUp(t)
	ibazel.Run([]string{}, "//:test")
	defer ibazel.Kill()

	ibazel.ExpectOutput("hello!", 50*time.Second)

	ioutil.WriteFile(
		filepath.Join(secondaryWd, "lib.sh"), []byte(secondaryLibAlt), 0777)
	ibazel.ExpectOutput("hello2!")
}

func TestRunWithRepositoryOverrideModifiedFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skipf("--override_repository is not implemented in windows")
	}

	ibazel := e2e.SetUp(t)
	ibazel.Run([]string{fmt.Sprintf("--override_repository=secondary=%s", secondaryWd2)}, "//:test")
	defer ibazel.Kill()

	ibazel.ExpectOutput("hello!")

	ioutil.WriteFile(
		filepath.Join(secondaryWd2, "lib.sh"), []byte(secondaryLibAlt), 0777)
	ibazel.ExpectOutput("hello2!")
}

func TestRunWithDisjointRepositoryOverrideModifiedFile(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("FSEvents is only used on macOS")
	}

	currentUser, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	repositoryRoot, err := os.MkdirTemp(currentUser.HomeDir, "ibazel-disjoint-root-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(repositoryRoot) })

	disjointRepository := filepath.Join(repositoryRoot, "secondary")
	if err := createSecondaryWorkspace(disjointRepository); err != nil {
		t.Fatal(err)
	}
	workspace, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	workspaceRoot := strings.Split(strings.Trim(workspace, "/"), "/")[0]
	repositoryTopLevel := strings.Split(strings.Trim(disjointRepository, "/"), "/")[0]
	if workspaceRoot == repositoryTopLevel {
		t.Fatalf("test paths share top-level directory %q", workspaceRoot)
	}

	ibazel := e2e.SetUp(t)
	ibazel.Run([]string{fmt.Sprintf("--override_repository=secondary=%s", disjointRepository)}, "//:test")
	defer ibazel.Kill()

	ibazel.ExpectOutput("hello!", 50*time.Second)

	if err := ioutil.WriteFile(filepath.Join(disjointRepository, "lib.sh"), []byte(secondaryLibAlt), 0777); err != nil {
		t.Fatal(err)
	}
	ibazel.ExpectOutput("hello2!")
}
