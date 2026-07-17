// Copyright 2017 The Bazel Authors. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package command

import (
	"bytes"
	"errors"
	"testing"

	"github.com/bazelbuild/bazel-watcher/internal/bazel"
	mock_bazel "github.com/bazelbuild/bazel-watcher/internal/bazel/testing"
	"github.com/bazelbuild/bazel-watcher/internal/ibazel/log"
	"github.com/bazelbuild/bazel-watcher/internal/ibazel/process_group"
)

type bufferWriteCloser struct {
	bytes.Buffer
}

func (b *bufferWriteCloser) Close() error { return nil }

func TestNotifyCommandStructuredBuildEvent(t *testing.T) {
	stdin := &bufferWriteCloser{}
	c := &notifyCommand{stdin: stdin, structured: true}
	changes := []Change{
		{Path: "/workspace/frontend/app.tsx", Kind: "source"},
		{Path: "/workspace/frontend/BUILD", Kind: "graph"},
	}

	c.writeBuildEvent(true, changes)

	want := "IBAZEL_EVENT {\"version\":1,\"type\":\"build_completed\",\"success\":true,\"changes\":[{\"path\":\"/workspace/frontend/app.tsx\",\"kind\":\"source\"},{\"path\":\"/workspace/frontend/BUILD\",\"kind\":\"graph\"}]}\n"
	if got := stdin.String(); got != want {
		t.Errorf("structured build event = %q, want %q", got, want)
	}
}

func TestNotifyCommandLegacyProtocolDoesNotWriteBuildEvent(t *testing.T) {
	stdin := &bufferWriteCloser{}
	c := &notifyCommand{stdin: stdin}

	c.writeBuildEvent(true, []Change{{Path: "/workspace/app.ts", Kind: "source"}})

	if got := stdin.String(); got != "" {
		t.Errorf("legacy protocol wrote structured event %q", got)
	}
}

func TestNotifyCommand(t *testing.T) {
	log.SetLogger(t)

	pg := process_group.Command("cat")

	c := &notifyCommand{
		args:      []string{"moo"},
		bazelArgs: []string{},
		pg:        pg,
		target:    "//path/to:target",
	}

	if c.IsSubprocessRunning() {
		t.Errorf("New subprocess shouldn't have been started yet. State: %v", pg.RootProcess().ProcessState)
	}

	var err error
	c.stdin, err = pg.RootProcess().StdinPipe()
	if err != nil {
		t.Error(err)
	}

	// Mock out bazel to return non-error on test
	b := &mock_bazel.MockBazel{}
	b.BuildError(nil)
	bazelNew = func() bazel.Bazel { return b }
	defer func() { bazelNew = oldBazelNew }()

	c.NotifyOfChanges(nil)
	b.BuildError(errors.New("Demo error"))
	c.NotifyOfChanges(nil)
	b.BuildError(nil)
	c.NotifyOfChanges(nil)

	b.AssertActions(t, [][]string{
		{"SetStartupArgs"},
		{"SetArguments"},
		{"WriteToStderr", "true"},
		{"WriteToStdout", "true"},
		{"Norun", "//path/to:target"},
		{"SetStartupArgs"},
		{"SetArguments"},
		{"WriteToStderr", "true"},
		{"WriteToStdout", "true"},
		{"Run", "--script_path=.*", "//path/to:target"},
		{"SetStartupArgs"},
		{"SetArguments"},
		{"WriteToStderr", "true"},
		{"WriteToStdout", "true"},
		{"Norun", "//path/to:target"},
		{"SetStartupArgs"},
		{"SetArguments"},
		{"WriteToStderr", "true"},
		{"WriteToStdout", "true"},
		{"Norun", "//path/to:target"},
		{"SetStartupArgs"},
		{"SetArguments"},
		{"WriteToStderr", "true"},
		{"WriteToStdout", "true"},
		{"Run", "--script_path=.*", "//path/to:target"},
	})
}

func TestNotifyCommand_Restart(t *testing.T) {
	log.SetLogger(t)

	var pg process_group.ProcessGroup

	pg = process_group.Command("ls")
	execCommand = func(name string, args ...string) process_group.ProcessGroup {
		return oldExecCommand("ls")
	}
	defer func() { execCommand = oldExecCommand }()

	c := &notifyCommand{
		args:      []string{"moo"},
		bazelArgs: []string{},
		pg:        pg,
		target:    "//path/to:target",
	}

	var err error
	c.stdin, err = pg.RootProcess().StdinPipe()
	if err != nil {
		t.Error(err)
	}

	b := &mock_bazel.MockBazel{}
	b.BuildError(errors.New("Demo error"))
	bazelNew = func() bazel.Bazel { return b }
	defer func() { bazelNew = oldBazelNew }()

	if c.IsSubprocessRunning() {
		t.Errorf("new subprocess shouldn't have been started yet. State: %v", pg.RootProcess().ProcessState)
	}

	c.NotifyOfChanges(nil)
	if c.IsSubprocessRunning() {
		t.Errorf("process should not start with build errors. State: %v", pg.RootProcess().ProcessState)
	}

	// Since the process isn't currently running, this should start it.
	b.BuildError(nil)
	c.NotifyOfChanges(nil)
	if !c.IsSubprocessRunning() {
		t.Errorf("subprocess should have started. State: %v", pg.RootProcess().ProcessState)
	}

	pid1 := c.pg.RootProcess().Process.Pid

	c.Terminate()
	if c.IsSubprocessRunning() {
		t.Errorf("subprocess should have been terminated. State: %v", pg.RootProcess().ProcessState)
	}

	b.BuildError(errors.New("Demo error"))
	c.NotifyOfChanges(nil)
	if c.IsSubprocessRunning() {
		t.Errorf("subprocess should not restart with build errors. State: %v", pg.RootProcess().ProcessState)
	}

	// Since the process isn't currently running, this should re-start it.
	b.BuildError(nil)
	c.NotifyOfChanges(nil)
	if !c.IsSubprocessRunning() {
		t.Errorf("subprocess should have been restarted. State: %v", pg.RootProcess().ProcessState)
	}

	pid2 := c.pg.RootProcess().Process.Pid
	if pid2 == pid1 {
		t.Error("PIDs of restarted process should be different that original process")
	}

	c.NotifyOfChanges(nil)
	if pid2 != c.pg.RootProcess().Process.Pid {
		t.Error("non-dead process was restarted")
	}
}
