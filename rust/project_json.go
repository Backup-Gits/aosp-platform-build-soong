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

package rust

import (
	"encoding/json"
	"fmt"
	"path"

	"android/soong/android"
)

// This singleton collects Rust crate definitions and generates a JSON file
// (${OUT_DIR}/soong/rust-project.json) which can be use by external tools,
// such as rust-analyzer. It does so when either make, mm, mma, mmm or mmma is
// called.  This singleton is enabled only if SOONG_GEN_RUST_PROJECT is set.
// For example,
//
//   $ SOONG_GEN_RUST_PROJECT=1 m nothing

const (
	// Environment variables used to control the behavior of this singleton.
	envVariableCollectRustDeps = "SOONG_GEN_RUST_PROJECT"
	rustProjectJsonFileName    = "rust-project.json"
)

// The format of rust-project.json is not yet finalized. A current description is available at:
// https://github.com/rust-analyzer/rust-analyzer/blob/master/docs/user/manual.adoc#non-cargo-based-projects
type rustProjectDep struct {
	// The Crate attribute is the index of the dependency in the Crates array in rustProjectJson.
	Crate int    `json:"crate"`
	Name  string `json:"name"`
}

type rustProjectCrate struct {
	RootModule string           `json:"root_module"`
	Edition    string           `json:"edition,omitempty"`
	Deps       []rustProjectDep `json:"deps"`
	Cfgs       []string         `json:"cfgs"`
}

type rustProjectJson struct {
	Roots  []string           `json:"roots"`
	Crates []rustProjectCrate `json:"crates"`
}

// crateInfo is used during the processing to keep track of the known crates.
type crateInfo struct {
	ID   int
	Deps map[string]int
}

type projectGeneratorSingleton struct {
	project     rustProjectJson
	knownCrates map[string]crateInfo
}

func rustProjectGeneratorSingleton() android.Singleton {
	return &projectGeneratorSingleton{}
}

func init() {
	android.RegisterSingletonType("rust_project_generator", rustProjectGeneratorSingleton)
}

// librarySource finds the main source file (.rs) for a crate.
func librarySource(ctx android.SingletonContext, rModule *Module, rustLib *libraryDecorator) (string, bool) {
	srcs := rustLib.baseCompiler.Properties.Srcs
	if len(srcs) != 0 {
		return path.Join(ctx.ModuleDir(rModule), srcs[0]), true
	}
	if !rustLib.source() {
		return "", false
	}
	// It is a SourceProvider module. If this module is host only, uses the variation for the host.
	// Otherwise, use the variation for the primary target.
	switch rModule.hod {
	case android.HostSupported:
	case android.HostSupportedNoCross:
		if rModule.Target().String() != ctx.Config().BuildOSTarget.String() {
			return "", false
		}
	default:
		if rModule.Target().String() != ctx.Config().Targets[android.Android][0].String() {
			return "", false
		}
	}
	src := rustLib.sourceProvider.Srcs()[0]
	return src.String(), true
}

func (singleton *projectGeneratorSingleton) mergeDependencies(ctx android.SingletonContext,
	module android.Module, crate *rustProjectCrate, deps map[string]int) {

	ctx.VisitDirectDeps(module, func(child android.Module) {
		childId, childCrateName, ok := singleton.appendLibraryAndDeps(ctx, child)
		if !ok {
			return
		}
		if _, ok = deps[ctx.ModuleName(child)]; ok {
			return
		}
		crate.Deps = append(crate.Deps, rustProjectDep{Crate: childId, Name: childCrateName})
		deps[ctx.ModuleName(child)] = childId
	})
}

// appendLibraryAndDeps creates a rustProjectCrate for the module argument and appends it to singleton.project.
// It visits the dependencies of the module depth-first so the dependency ID can be added to the current module. If the
// current module is already in singleton.knownCrates, its dependencies are merged. Returns a tuple (id, crate_name, ok).
func (singleton *projectGeneratorSingleton) appendLibraryAndDeps(ctx android.SingletonContext, module android.Module) (int, string, bool) {
	rModule, ok := module.(*Module)
	if !ok {
		return 0, "", false
	}
	if rModule.compiler == nil {
		return 0, "", false
	}
	rustLib, ok := rModule.compiler.(*libraryDecorator)
	if !ok {
		return 0, "", false
	}
	moduleName := ctx.ModuleName(module)
	crateName := rModule.CrateName()
	if cInfo, ok := singleton.knownCrates[moduleName]; ok {
		// We have seen this crate already; merge any new dependencies.
		crate := singleton.project.Crates[cInfo.ID]
		singleton.mergeDependencies(ctx, module, &crate, cInfo.Deps)
		singleton.project.Crates[cInfo.ID] = crate
		return cInfo.ID, crateName, true
	}
	crate := rustProjectCrate{Deps: make([]rustProjectDep, 0), Cfgs: make([]string, 0)}
	rootModule, ok := librarySource(ctx, rModule, rustLib)
	if !ok {
		return 0, "", false
	}
	crate.RootModule = rootModule
	crate.Edition = rustLib.baseCompiler.edition()

	deps := make(map[string]int)
	singleton.mergeDependencies(ctx, module, &crate, deps)

	id := len(singleton.project.Crates)
	singleton.knownCrates[moduleName] = crateInfo{ID: id, Deps: deps}
	singleton.project.Crates = append(singleton.project.Crates, crate)
	// rust-analyzer requires that all crates belong to at least one root:
	// https://github.com/rust-analyzer/rust-analyzer/issues/4735.
	singleton.project.Roots = append(singleton.project.Roots, path.Dir(crate.RootModule))
	return id, crateName, true
}

func (singleton *projectGeneratorSingleton) GenerateBuildActions(ctx android.SingletonContext) {
	if !ctx.Config().IsEnvTrue(envVariableCollectRustDeps) {
		return
	}

	singleton.knownCrates = make(map[string]crateInfo)
	ctx.VisitAllModules(func(module android.Module) {
		singleton.appendLibraryAndDeps(ctx, module)
	})

	path := android.PathForOutput(ctx, rustProjectJsonFileName)
	err := createJsonFile(singleton.project, path)
	if err != nil {
		ctx.Errorf(err.Error())
	}
}

func createJsonFile(project rustProjectJson, rustProjectPath android.WritablePath) error {
	buf, err := json.MarshalIndent(project, "", "  ")
	if err != nil {
		return fmt.Errorf("JSON marshal of rustProjectJson failed: %s", err)
	}
	err = android.WriteFileToOutputDir(rustProjectPath, buf, 0666)
	if err != nil {
		return fmt.Errorf("Writing rust-project to %s failed: %s", rustProjectPath.String(), err)
	}
	return nil
}
