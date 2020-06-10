// Copyright 2016 Google Inc. All rights reserved.
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

package android

import (
	"fmt"
	"testing"

	"github.com/google/blueprint"
)

var prebuiltsTests = []struct {
	name     string
	modules  string
	prebuilt []OsClass
}{
	{
		name: "no prebuilt",
		modules: `
			source {
				name: "bar",
			}`,
		prebuilt: nil,
	},
	{
		name: "no source prebuilt not preferred",
		modules: `
			prebuilt {
				name: "bar",
				prefer: false,
				srcs: ["prebuilt_file"],
			}`,
		prebuilt: []OsClass{Device, Host},
	},
	{
		name: "no source prebuilt preferred",
		modules: `
			prebuilt {
				name: "bar",
				prefer: true,
				srcs: ["prebuilt_file"],
			}`,
		prebuilt: []OsClass{Device, Host},
	},
	{
		name: "prebuilt not preferred",
		modules: `
			source {
				name: "bar",
			}

			prebuilt {
				name: "bar",
				prefer: false,
				srcs: ["prebuilt_file"],
			}`,
		prebuilt: nil,
	},
	{
		name: "prebuilt preferred",
		modules: `
			source {
				name: "bar",
			}

			prebuilt {
				name: "bar",
				prefer: true,
				srcs: ["prebuilt_file"],
			}`,
		prebuilt: []OsClass{Device, Host},
	},
	{
		name: "prebuilt no file not preferred",
		modules: `
			source {
				name: "bar",
			}

			prebuilt {
				name: "bar",
				prefer: false,
			}`,
		prebuilt: nil,
	},
	{
		name: "prebuilt no file preferred",
		modules: `
			source {
				name: "bar",
			}

			prebuilt {
				name: "bar",
				prefer: true,
			}`,
		prebuilt: nil,
	},
	{
		name: "prebuilt file from filegroup preferred",
		modules: `
			filegroup {
				name: "fg",
				srcs: ["prebuilt_file"],
			}
			prebuilt {
				name: "bar",
				prefer: true,
				srcs: [":fg"],
			}`,
		prebuilt: []OsClass{Device, Host},
	},
	{
		name: "prebuilt module for device only",
		modules: `
			source {
				name: "bar",
			}

			prebuilt {
				name: "bar",
				host_supported: false,
				prefer: true,
				srcs: ["prebuilt_file"],
			}`,
		prebuilt: []OsClass{Device},
	},
	{
		name: "prebuilt file for host only",
		modules: `
			source {
				name: "bar",
			}

			prebuilt {
				name: "bar",
				prefer: true,
				target: {
					host: {
						srcs: ["prebuilt_file"],
					},
				},
			}`,
		prebuilt: []OsClass{Host},
	},
}

func TestPrebuilts(t *testing.T) {
	fs := map[string][]byte{
		"prebuilt_file": nil,
		"source_file":   nil,
	}

	for _, test := range prebuiltsTests {
		t.Run(test.name, func(t *testing.T) {
			bp := `
				source {
					name: "foo",
					deps: [":bar"],
				}
				` + test.modules
			config := TestArchConfig(buildDir, nil, bp, fs)

			ctx := NewTestArchContext()
			registerTestPrebuiltBuildComponents(ctx)
			ctx.RegisterModuleType("filegroup", FileGroupFactory)
			ctx.Register(config)

			_, errs := ctx.ParseBlueprintsFiles("Android.bp")
			FailIfErrored(t, errs)
			_, errs = ctx.PrepareBuildActions(config)
			FailIfErrored(t, errs)

			for _, variant := range ctx.ModuleVariantsForTests("foo") {
				foo := ctx.ModuleForTests("foo", variant)
				t.Run(foo.Module().Target().Os.Class.String(), func(t *testing.T) {
					var dependsOnSourceModule, dependsOnPrebuiltModule bool
					ctx.VisitDirectDeps(foo.Module(), func(m blueprint.Module) {
						if _, ok := m.(*sourceModule); ok {
							dependsOnSourceModule = true
						}
						if p, ok := m.(*prebuiltModule); ok {
							dependsOnPrebuiltModule = true
							if !p.Prebuilt().properties.UsePrebuilt {
								t.Errorf("dependency on prebuilt module not marked used")
							}
						}
					})

					deps := foo.Module().(*sourceModule).deps
					if deps == nil || len(deps) != 1 {
						t.Errorf("deps does not have single path, but is %v", deps)
					}
					var usingSourceFile, usingPrebuiltFile bool
					if deps[0].String() == "source_file" {
						usingSourceFile = true
					}
					if deps[0].String() == "prebuilt_file" {
						usingPrebuiltFile = true
					}

					prebuilt := false
					for _, os := range test.prebuilt {
						if os == foo.Module().Target().Os.Class {
							prebuilt = true
						}
					}

					if prebuilt {
						if !dependsOnPrebuiltModule {
							t.Errorf("doesn't depend on prebuilt module")
						}
						if !usingPrebuiltFile {
							t.Errorf("doesn't use prebuilt_file")
						}

						if dependsOnSourceModule {
							t.Errorf("depends on source module")
						}
						if usingSourceFile {
							t.Errorf("using source_file")
						}
					} else {
						if dependsOnPrebuiltModule {
							t.Errorf("depends on prebuilt module")
						}
						if usingPrebuiltFile {
							t.Errorf("using prebuilt_file")
						}

						if !dependsOnSourceModule {
							t.Errorf("doesn't depend on source module")
						}
						if !usingSourceFile {
							t.Errorf("doesn't use source_file")
						}
					}
				})
			}
		})
	}
}

func registerTestPrebuiltBuildComponents(ctx RegistrationContext) {
	ctx.RegisterModuleType("prebuilt", newPrebuiltModule)
	ctx.RegisterModuleType("source", newSourceModule)

	RegisterPrebuiltMutators(ctx)
}

type prebuiltModule struct {
	ModuleBase
	prebuilt   Prebuilt
	properties struct {
		Srcs []string `android:"path,arch_variant"`
	}
	src Path
}

func newPrebuiltModule() Module {
	m := &prebuiltModule{}
	m.AddProperties(&m.properties)
	InitPrebuiltModule(m, &m.properties.Srcs)
	InitAndroidArchModule(m, HostAndDeviceDefault, MultilibCommon)
	return m
}

func (p *prebuiltModule) Name() string {
	return p.prebuilt.Name(p.ModuleBase.Name())
}

func (p *prebuiltModule) GenerateAndroidBuildActions(ctx ModuleContext) {
	if len(p.properties.Srcs) >= 1 {
		p.src = p.prebuilt.SingleSourcePath(ctx)
	}
}

func (p *prebuiltModule) Prebuilt() *Prebuilt {
	return &p.prebuilt
}

func (p *prebuiltModule) OutputFiles(tag string) (Paths, error) {
	switch tag {
	case "":
		return Paths{p.src}, nil
	default:
		return nil, fmt.Errorf("unsupported module reference tag %q", tag)
	}
}

type sourceModule struct {
	ModuleBase
	properties struct {
		Deps []string `android:"path,arch_variant"`
	}
	dependsOnSourceModule, dependsOnPrebuiltModule bool
	deps                                           Paths
	src                                            Path
}

func newSourceModule() Module {
	m := &sourceModule{}
	m.AddProperties(&m.properties)
	InitAndroidArchModule(m, HostAndDeviceDefault, MultilibCommon)
	return m
}

func (s *sourceModule) DepsMutator(ctx BottomUpMutatorContext) {
	// s.properties.Deps are annotated with android:path, so they are
	// automatically added to the dependency by pathDeps mutator
}

func (s *sourceModule) GenerateAndroidBuildActions(ctx ModuleContext) {
	s.deps = PathsForModuleSrc(ctx, s.properties.Deps)
	s.src = PathForModuleSrc(ctx, "source_file")
}

func (s *sourceModule) Srcs() Paths {
	return Paths{s.src}
}
