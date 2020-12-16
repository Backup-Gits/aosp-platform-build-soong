// Copyright 2020 Google Inc. All rights reserved.
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

package bazel

import (
	"encoding/json"
	"strings"

	"github.com/google/blueprint/proptools"
)

// artifact contains relevant portions of Bazel's aquery proto, Artifact.
// Represents a single artifact, whether it's a source file or a derived output file.
type artifact struct {
	Id       string
	ExecPath string
}

// KeyValuePair represents Bazel's aquery proto, KeyValuePair.
type KeyValuePair struct {
	Key   string
	Value string
}

// depSetOfFiles contains relevant portions of Bazel's aquery proto, DepSetOfFiles.
// Represents a data structure containing one or more files. Depsets in Bazel are an efficient
// data structure for storing large numbers of file paths.
type depSetOfFiles struct {
	Id string
	// TODO(cparsons): Handle non-flat depsets.
	DirectArtifactIds []string
}

// action contains relevant portions of Bazel's aquery proto, Action.
// Represents a single command line invocation in the Bazel build graph.
type action struct {
	Arguments            []string
	EnvironmentVariables []KeyValuePair
	InputDepSetIds       []string
	Mnemonic             string
	OutputIds            []string
}

// actionGraphContainer contains relevant portions of Bazel's aquery proto, ActionGraphContainer.
// An aquery response from Bazel contains a single ActionGraphContainer proto.
type actionGraphContainer struct {
	Artifacts     []artifact
	Actions       []action
	DepSetOfFiles []depSetOfFiles
}

// BuildStatement contains information to register a build statement corresponding (one to one)
// with a Bazel action from Bazel's action graph.
type BuildStatement struct {
	Command     string
	OutputPaths []string
	InputPaths  []string
	Env         []KeyValuePair
	Mnemonic    string
}

// AqueryBuildStatements returns an array of BuildStatements which should be registered (and output
// to a ninja file) to correspond one-to-one with the given action graph json proto (from a bazel
// aquery invocation).
func AqueryBuildStatements(aqueryJsonProto []byte) []BuildStatement {
	buildStatements := []BuildStatement{}

	var aqueryResult actionGraphContainer
	json.Unmarshal(aqueryJsonProto, &aqueryResult)

	artifactIdToPath := map[string]string{}
	for _, artifact := range aqueryResult.Artifacts {
		artifactIdToPath[artifact.Id] = artifact.ExecPath
	}
	depsetIdToArtifactIds := map[string][]string{}
	for _, depset := range aqueryResult.DepSetOfFiles {
		depsetIdToArtifactIds[depset.Id] = depset.DirectArtifactIds
	}

	for _, actionEntry := range aqueryResult.Actions {
		outputPaths := []string{}
		for _, outputId := range actionEntry.OutputIds {
			// TODO(cparsons): Validate the id is present.
			outputPaths = append(outputPaths, artifactIdToPath[outputId])
		}
		inputPaths := []string{}
		for _, inputDepSetId := range actionEntry.InputDepSetIds {
			// TODO(cparsons): Validate the id is present.
			for _, inputId := range depsetIdToArtifactIds[inputDepSetId] {
				// TODO(cparsons): Validate the id is present.
				inputPaths = append(inputPaths, artifactIdToPath[inputId])
			}
		}
		buildStatement := BuildStatement{
			Command:     strings.Join(proptools.ShellEscapeList(actionEntry.Arguments), " "),
			OutputPaths: outputPaths,
			InputPaths:  inputPaths,
			Env:         actionEntry.EnvironmentVariables,
			Mnemonic:    actionEntry.Mnemonic}
		buildStatements = append(buildStatements, buildStatement)
	}

	return buildStatements
}
