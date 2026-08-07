// Copyright 2026 The Bazel Authors. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package bep reads the subset of Bazel's JSON Build Event Protocol needed by
// notification-mode commands.
package bep

import (
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
)

// Output identifies one artifact in a Bazel output group. Path is its logical
// execroot-relative path; URI locates the materialized artifact when available.
type Output struct {
	Path   string `json:"path"`
	URI    string `json:"uri,omitempty"`
	Digest string `json:"digest,omitempty"`
}

type event struct {
	ID              eventID          `json:"id"`
	NamedSetOfFiles *namedSetOfFiles `json:"namedSetOfFiles"`
	Completed       *targetComplete  `json:"completed"`
}

type eventID struct {
	NamedSet *namedSetID `json:"namedSet"`
}

type namedSetID struct {
	ID string `json:"id"`
}

type namedSetOfFiles struct {
	Files    []file       `json:"files"`
	FileSets []namedSetID `json:"fileSets"`
}

type file struct {
	Name       string   `json:"name"`
	URI        string   `json:"uri"`
	PathPrefix []string `json:"pathPrefix"`
	Digest     string   `json:"digest"`
}

type targetComplete struct {
	OutputGroups []outputGroup `json:"outputGroup"`
}

type outputGroup struct {
	Name       string       `json:"name"`
	FileSets   []namedSetID `json:"fileSets"`
	Incomplete bool         `json:"incomplete"`
}

// ReadOutputGroups extracts selected output groups from a JSON BEP stream.
func ReadOutputGroups(reader io.Reader, selected []string) (map[string][]Output, error) {
	wanted := make(map[string]struct{}, len(selected))
	groups := make(map[string][]string, len(selected))
	seenGroups := make(map[string]struct{}, len(selected))
	for _, name := range selected {
		wanted[name] = struct{}{}
		groups[name] = nil
	}

	namedSets := make(map[string]namedSetOfFiles)
	decoder := json.NewDecoder(reader)
	for {
		var current event
		if err := decoder.Decode(&current); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decode BEP: %w", err)
		}

		if current.ID.NamedSet != nil && current.NamedSetOfFiles != nil {
			namedSets[current.ID.NamedSet.ID] = *current.NamedSetOfFiles
		}
		if current.Completed == nil {
			continue
		}
		for _, group := range current.Completed.OutputGroups {
			if _, ok := wanted[group.Name]; !ok {
				continue
			}
			if group.Incomplete {
				return nil, fmt.Errorf("output group %q is incomplete", group.Name)
			}
			seenGroups[group.Name] = struct{}{}
			for _, fileSet := range group.FileSets {
				groups[group.Name] = append(groups[group.Name], fileSet.ID)
			}
		}
	}

	outputs := make(map[string][]Output, len(groups))
	for name, roots := range groups {
		if _, ok := seenGroups[name]; !ok {
			return nil, fmt.Errorf("output group %q was not reported", name)
		}
		files, err := expandNamedSets(namedSets, roots)
		if err != nil {
			return nil, fmt.Errorf("read output group %q: %w", name, err)
		}
		outputs[name] = files
	}
	return outputs, nil
}

func expandNamedSets(namedSets map[string]namedSetOfFiles, roots []string) ([]Output, error) {
	seenSets := make(map[string]struct{})
	type outputKey struct {
		Path string
		URI  string
	}
	seenFiles := make(map[outputKey]Output)
	stack := append([]string(nil), roots...)
	for len(stack) > 0 {
		last := len(stack) - 1
		id := stack[last]
		stack = stack[:last]
		if _, ok := seenSets[id]; ok {
			continue
		}
		seenSets[id] = struct{}{}

		set, ok := namedSets[id]
		if !ok {
			return nil, fmt.Errorf("missing named set %q", id)
		}
		for _, item := range set.Files {
			output := Output{
				Path:   path.Join(append(item.PathPrefix, item.Name)...),
				URI:    item.URI,
				Digest: item.Digest,
			}
			key := outputKey{Path: output.Path, URI: output.URI}
			if previous, ok := seenFiles[key]; ok && previous.Digest != output.Digest {
				return nil, fmt.Errorf("artifact %q has conflicting digests", output.Path)
			}
			seenFiles[key] = output
		}
		for _, child := range set.FileSets {
			stack = append(stack, child.ID)
		}
	}

	outputs := make([]Output, 0, len(seenFiles))
	for _, output := range seenFiles {
		outputs = append(outputs, output)
	}
	sort.Slice(outputs, func(i, j int) bool {
		if outputs[i].Path == outputs[j].Path {
			return outputs[i].URI < outputs[j].URI
		}
		return outputs[i].Path < outputs[j].Path
	})
	return outputs, nil
}
