package simple

import (
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/bazelbuild/bazel-watcher/internal/e2e"
)

const nestedBuild = `
exports_files(["exclaim.sh"])
`

const nestedScript = `
printf "!"
`

const nestedModuleFile = `
module(
	name = "nested_module",
	repo_name = "nested",
)
`

const mainFiles = `
-- BUILD --
genrule(
    name = "gen_test",
    srcs = ["@nested//:exclaim.sh", "greeting.sh"],
    outs = ["test.sh"],
    cmd = "cat \"$(location greeting.sh)\" \"$(location @nested//:exclaim.sh)\" > \"$@\"",
    executable = True,
)
sh_binary(
    name = "test",
    srcs = ["test.sh"],
)
-- greeting.sh --
printf "hello"
`

const mainModuleFile = `
module(name = "primary")
bazel_dep(name = "nested_module", repo_name = "nested")
local_path_override(
    module_name = "nested_module",
    path = "./nested/",
)
`

const greetingAlt = `
printf "hello2"
`

var nestedWd string

func TestMain(m *testing.M) {
	e2e.TestMain(m, e2e.Args{
		Main:              mainFiles,
		ModuleFileContent: mainModuleFile,
		SetUp: func() error {
			// Create a nested module in a subfolder.
			nestedWd, _ = filepath.Abs("nested")

			// Manually create files in the nested module.
			if err := os.Mkdir(nestedWd, 0777); err != nil {
				log.Fatalf("os.Mkdir(%q): %v", nestedWd, err)
			}
			for file, contents := range map[string]string{
				"BUILD.bazel":   nestedBuild,
				"exclaim.sh":    nestedScript,
				"MODULE.bazel":  nestedModuleFile,
			} {
				if err := ioutil.WriteFile(filepath.Join(nestedWd, file), []byte(contents), 0777); err != nil {
					log.Fatalf("Failed to write file %q: %v", file, err)
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

	ibazel.ExpectOutput("hello!")

	ioutil.WriteFile("greeting.sh", []byte(greetingAlt), 0777)
	ibazel.ExpectOutput("hello2!")
}
