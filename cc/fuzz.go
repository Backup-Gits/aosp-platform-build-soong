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

package cc

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/google/blueprint/proptools"

	"android/soong/android"
	"android/soong/cc/config"
)

type FuzzConfig struct {
	// Email address of people to CC on bugs or contact about this fuzz target.
	Cc []string `json:"cc,omitempty"`
	// Boolean specifying whether to disable the fuzz target from running
	// automatically in continuous fuzzing infrastructure.
	Disable *bool `json:"disable,omitempty"`
	// Component in Google's bug tracking system that bugs should be filed to.
	Componentid *int64 `json:"componentid,omitempty"`
	// Hotlists in Google's bug tracking system that bugs should be marked with.
	Hotlists []string `json:"hotlists,omitempty"`
}

func (f *FuzzConfig) String() string {
	b, err := json.Marshal(f)
	if err != nil {
		panic(err)
	}

	return string(b)
}

type FuzzProperties struct {
	// Optional list of seed files to be installed to the fuzz target's output
	// directory.
	Corpus []string `android:"path"`
	// Optional dictionary to be installed to the fuzz target's output directory.
	Dictionary *string `android:"path"`
	// Config for running the target on fuzzing infrastructure.
	Fuzz_config *FuzzConfig
}

func init() {
	android.RegisterModuleType("cc_fuzz", FuzzFactory)
	android.RegisterSingletonType("cc_fuzz_packaging", fuzzPackagingFactory)
}

// cc_fuzz creates a host/device fuzzer binary. Host binaries can be found at
// $ANDROID_HOST_OUT/fuzz/, and device binaries can be found at /data/fuzz on
// your device, or $ANDROID_PRODUCT_OUT/data/fuzz in your build tree.
func FuzzFactory() android.Module {
	module := NewFuzz(android.HostAndDeviceSupported)
	return module.Init()
}

func NewFuzzInstaller() *baseInstaller {
	return NewBaseInstaller("fuzz", "fuzz", InstallInData)
}

type fuzzBinary struct {
	*binaryDecorator
	*baseCompiler

	Properties            FuzzProperties
	dictionary            android.Path
	corpus                android.Paths
	corpusIntermediateDir android.Path
	config                android.Path
}

func (fuzz *fuzzBinary) linkerProps() []interface{} {
	props := fuzz.binaryDecorator.linkerProps()
	props = append(props, &fuzz.Properties)
	return props
}

func (fuzz *fuzzBinary) linkerInit(ctx BaseModuleContext) {
	// Add ../lib[64] to rpath so that out/host/linux-x86/fuzz/<fuzzer> can
	// find out/host/linux-x86/lib[64]/library.so
	runpaths := []string{"../lib"}
	for _, runpath := range runpaths {
		if ctx.toolchain().Is64Bit() {
			runpath += "64"
		}
		fuzz.binaryDecorator.baseLinker.dynamicProperties.RunPaths = append(
			fuzz.binaryDecorator.baseLinker.dynamicProperties.RunPaths, runpath)
	}

	// add "" to rpath so that fuzzer binaries can find libraries in their own fuzz directory
	fuzz.binaryDecorator.baseLinker.dynamicProperties.RunPaths = append(
		fuzz.binaryDecorator.baseLinker.dynamicProperties.RunPaths, "")

	fuzz.binaryDecorator.linkerInit(ctx)
}

func (fuzz *fuzzBinary) linkerDeps(ctx DepsContext, deps Deps) Deps {
	deps.StaticLibs = append(deps.StaticLibs,
		config.LibFuzzerRuntimeLibrary(ctx.toolchain()))
	deps = fuzz.binaryDecorator.linkerDeps(ctx, deps)
	return deps
}

func (fuzz *fuzzBinary) linkerFlags(ctx ModuleContext, flags Flags) Flags {
	flags = fuzz.binaryDecorator.linkerFlags(ctx, flags)
	return flags
}

func (fuzz *fuzzBinary) install(ctx ModuleContext, file android.Path) {
	fuzz.binaryDecorator.baseInstaller.dir = filepath.Join(
		"fuzz", ctx.Target().Arch.ArchType.String(), ctx.ModuleName())
	fuzz.binaryDecorator.baseInstaller.dir64 = filepath.Join(
		"fuzz", ctx.Target().Arch.ArchType.String(), ctx.ModuleName())
	fuzz.binaryDecorator.baseInstaller.install(ctx, file)

	fuzz.corpus = android.PathsForModuleSrc(ctx, fuzz.Properties.Corpus)
	builder := android.NewRuleBuilder()
	intermediateDir := android.PathForModuleOut(ctx, "corpus")
	for _, entry := range fuzz.corpus {
		builder.Command().Text("cp").
			Input(entry).
			Output(intermediateDir.Join(ctx, entry.Base()))
	}
	builder.Build(pctx, ctx, "copy_corpus", "copy corpus")
	fuzz.corpusIntermediateDir = intermediateDir

	if fuzz.Properties.Dictionary != nil {
		fuzz.dictionary = android.PathForModuleSrc(ctx, *fuzz.Properties.Dictionary)
		if fuzz.dictionary.Ext() != ".dict" {
			ctx.PropertyErrorf("dictionary",
				"Fuzzer dictionary %q does not have '.dict' extension",
				fuzz.dictionary.String())
		}
	}

	if fuzz.Properties.Fuzz_config != nil {
		configPath := android.PathForModuleOut(ctx, "config").Join(ctx, "config.json")
		ctx.Build(pctx, android.BuildParams{
			Rule:        android.WriteFile,
			Description: "fuzzer infrastructure configuration",
			Output:      configPath,
			Args: map[string]string{
				"content": fuzz.Properties.Fuzz_config.String(),
			},
		})
		fuzz.config = configPath
	}
}

func NewFuzz(hod android.HostOrDeviceSupported) *Module {
	module, binary := NewBinary(hod)

	binary.baseInstaller = NewFuzzInstaller()
	module.sanitize.SetSanitizer(fuzzer, true)

	fuzz := &fuzzBinary{
		binaryDecorator: binary,
		baseCompiler:    NewBaseCompiler(),
	}
	module.compiler = fuzz
	module.linker = fuzz
	module.installer = fuzz

	// The fuzzer runtime is not present for darwin host modules, disable cc_fuzz modules when targeting darwin.
	android.AddLoadHook(module, func(ctx android.LoadHookContext) {
		disableDarwinAndLinuxBionic := struct {
			Target struct {
				Darwin struct {
					Enabled *bool
				}
				Linux_bionic struct {
					Enabled *bool
				}
			}
		}{}
		disableDarwinAndLinuxBionic.Target.Darwin.Enabled = BoolPtr(false)
		disableDarwinAndLinuxBionic.Target.Linux_bionic.Enabled = BoolPtr(false)
		ctx.AppendProperties(&disableDarwinAndLinuxBionic)
	})

	// Statically link the STL. This allows fuzz target deployment to not have to
	// include the STL.
	android.AddLoadHook(module, func(ctx android.LoadHookContext) {
		staticStlLinkage := struct {
			Target struct {
				Linux_glibc struct {
					Stl *string
				}
			}
		}{}

		staticStlLinkage.Target.Linux_glibc.Stl = proptools.StringPtr("libc++_static")
		ctx.AppendProperties(&staticStlLinkage)
	})

	return module
}

// Responsible for generating GNU Make rules that package fuzz targets into
// their architecture & target/host specific zip file.
type fuzzPackager struct {
	packages android.Paths
}

func fuzzPackagingFactory() android.Singleton {
	return &fuzzPackager{}
}

type fileToZip struct {
	SourceFilePath        android.Path
	DestinationPathPrefix string
}

func (s *fuzzPackager) GenerateBuildActions(ctx android.SingletonContext) {
	// Map between each architecture + host/device combination, and the files that
	// need to be packaged (in the tuple of {source file, destination folder in
	// archive}).
	archDirs := make(map[android.OutputPath][]fileToZip)

	ctx.VisitAllModules(func(module android.Module) {
		// Discard non-fuzz targets.
		ccModule, ok := module.(*Module)
		if !ok {
			return
		}
		fuzzModule, ok := ccModule.compiler.(*fuzzBinary)
		if !ok {
			return
		}

		// Discard vendor-NDK-linked modules, they're duplicates of fuzz targets
		// we're going to package anyway.
		if ccModule.UseVndk() || !ccModule.Enabled() {
			return
		}

		hostOrTargetString := "target"
		if ccModule.Host() {
			hostOrTargetString = "host"
		}

		archString := ccModule.Arch().ArchType.String()
		archDir := android.PathForIntermediates(ctx, "fuzz", hostOrTargetString, archString)

		// The executable.
		archDirs[archDir] = append(archDirs[archDir],
			fileToZip{ccModule.UnstrippedOutputFile(), ccModule.Name()})

		// The corpora.
		for _, corpusEntry := range fuzzModule.corpus {
			archDirs[archDir] = append(archDirs[archDir],
				fileToZip{corpusEntry, ccModule.Name() + "/corpus"})
		}

		// The dictionary.
		if fuzzModule.dictionary != nil {
			archDirs[archDir] = append(archDirs[archDir],
				fileToZip{fuzzModule.dictionary, ccModule.Name()})
		}

		// Additional fuzz config.
		if fuzzModule.config != nil {
			archDirs[archDir] = append(archDirs[archDir],
				fileToZip{fuzzModule.config, ccModule.Name()})
		}
	})

	for archDir, filesToZip := range archDirs {
		arch := archDir.Base()
		hostOrTarget := filepath.Base(filepath.Dir(archDir.String()))
		builder := android.NewRuleBuilder()
		outputFile := android.PathForOutput(ctx, "fuzz-"+hostOrTarget+"-"+arch+".zip")
		s.packages = append(s.packages, outputFile)

		command := builder.Command().BuiltTool(ctx, "soong_zip").
			Flag("-j").
			FlagWithOutput("-o ", outputFile)

		for _, fileToZip := range filesToZip {
			command.FlagWithArg("-P ", fileToZip.DestinationPathPrefix).
				FlagWithInput("-f ", fileToZip.SourceFilePath)
		}

		builder.Build(pctx, ctx, "create-fuzz-package-"+arch+"-"+hostOrTarget,
			"Create fuzz target packages for "+arch+"-"+hostOrTarget)
	}
}

func (s *fuzzPackager) MakeVars(ctx android.MakeVarsContext) {
	// TODO(mitchp): Migrate this to use MakeVarsContext::DistForGoal() when it's
	// ready to handle phony targets created in Soong. In the meantime, this
	// exports the phony 'fuzz' target and dependencies on packages to
	// core/main.mk so that we can use dist-for-goals.
	ctx.Strict("SOONG_FUZZ_PACKAGING_ARCH_MODULES", strings.Join(s.packages.Strings(), " "))
}
