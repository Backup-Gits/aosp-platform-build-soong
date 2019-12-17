// Copyright 2019 Google Inc. All rights reserved.
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

package sdk

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"android/soong/android"
	"android/soong/apex"
	"android/soong/cc"
	"android/soong/java"
)

func testSdkContext(bp string, fs map[string][]byte) (*android.TestContext, android.Config) {
	config := android.TestArchConfig(buildDir, nil)
	ctx := android.NewTestArchContext()

	// from android package
	ctx.PreArchMutators(android.RegisterPackageRenamer)
	ctx.PreArchMutators(android.RegisterVisibilityRuleChecker)
	ctx.PreArchMutators(android.RegisterDefaultsPreArchMutators)
	ctx.PreArchMutators(android.RegisterVisibilityRuleGatherer)
	ctx.PostDepsMutators(android.RegisterVisibilityRuleEnforcer)

	ctx.PreArchMutators(func(ctx android.RegisterMutatorsContext) {
		ctx.BottomUp("prebuilts", android.PrebuiltMutator).Parallel()
	})
	ctx.PostDepsMutators(func(ctx android.RegisterMutatorsContext) {
		ctx.TopDown("prebuilt_select", android.PrebuiltSelectModuleMutator).Parallel()
		ctx.BottomUp("prebuilt_postdeps", android.PrebuiltPostDepsMutator).Parallel()
	})
	ctx.RegisterModuleType("package", android.PackageFactory)

	// from java package
	ctx.RegisterModuleType("android_app_certificate", java.AndroidAppCertificateFactory)
	ctx.RegisterModuleType("java_defaults", java.DefaultsFactory)
	ctx.RegisterModuleType("java_library", java.LibraryFactory)
	ctx.RegisterModuleType("java_import", java.ImportFactory)
	ctx.RegisterModuleType("droidstubs", java.DroidstubsFactory)
	ctx.RegisterModuleType("prebuilt_stubs_sources", java.PrebuiltStubsSourcesFactory)

	// from cc package
	ctx.RegisterModuleType("cc_library", cc.LibraryFactory)
	ctx.RegisterModuleType("cc_library_shared", cc.LibrarySharedFactory)
	ctx.RegisterModuleType("cc_library_static", cc.LibraryStaticFactory)
	ctx.RegisterModuleType("cc_object", cc.ObjectFactory)
	ctx.RegisterModuleType("cc_prebuilt_library_shared", cc.PrebuiltSharedLibraryFactory)
	ctx.RegisterModuleType("cc_prebuilt_library_static", cc.PrebuiltStaticLibraryFactory)
	ctx.RegisterModuleType("llndk_library", cc.LlndkLibraryFactory)
	ctx.RegisterModuleType("toolchain_library", cc.ToolchainLibraryFactory)
	ctx.PreDepsMutators(func(ctx android.RegisterMutatorsContext) {
		ctx.BottomUp("link", cc.LinkageMutator).Parallel()
		ctx.BottomUp("vndk", cc.VndkMutator).Parallel()
		ctx.BottomUp("test_per_src", cc.TestPerSrcMutator).Parallel()
		ctx.BottomUp("version", cc.VersionMutator).Parallel()
		ctx.BottomUp("begin", cc.BeginMutator).Parallel()
	})

	// from apex package
	ctx.RegisterModuleType("apex", apex.BundleFactory)
	ctx.RegisterModuleType("apex_key", apex.ApexKeyFactory)
	ctx.PostDepsMutators(apex.RegisterPostDepsMutators)

	// from this package
	ctx.RegisterModuleType("sdk", ModuleFactory)
	ctx.RegisterModuleType("sdk_snapshot", SnapshotModuleFactory)
	ctx.PreDepsMutators(RegisterPreDepsMutators)
	ctx.PostDepsMutators(RegisterPostDepsMutators)

	ctx.Register()

	bp = bp + `
		apex_key {
			name: "myapex.key",
			public_key: "myapex.avbpubkey",
			private_key: "myapex.pem",
		}

		android_app_certificate {
			name: "myapex.cert",
			certificate: "myapex",
		}
	` + cc.GatherRequiredDepsForTest(android.Android)

	mockFS := map[string][]byte{
		"Android.bp":                                 []byte(bp),
		"build/make/target/product/security":         nil,
		"apex_manifest.json":                         nil,
		"system/sepolicy/apex/myapex-file_contexts":  nil,
		"system/sepolicy/apex/myapex2-file_contexts": nil,
		"myapex.avbpubkey":                           nil,
		"myapex.pem":                                 nil,
		"myapex.x509.pem":                            nil,
		"myapex.pk8":                                 nil,
	}

	for k, v := range fs {
		mockFS[k] = v
	}

	ctx.MockFileSystem(mockFS)

	return ctx, config
}

func testSdkWithFs(t *testing.T, bp string, fs map[string][]byte) *testSdkResult {
	t.Helper()
	ctx, config := testSdkContext(bp, fs)
	_, errs := ctx.ParseBlueprintsFiles(".")
	android.FailIfErrored(t, errs)
	_, errs = ctx.PrepareBuildActions(config)
	android.FailIfErrored(t, errs)
	return &testSdkResult{
		TestHelper: TestHelper{t: t},
		ctx:        ctx,
		config:     config,
	}
}

func testSdkError(t *testing.T, pattern, bp string) {
	t.Helper()
	ctx, config := testSdkContext(bp, nil)
	_, errs := ctx.ParseFileList(".", []string{"Android.bp"})
	if len(errs) > 0 {
		android.FailIfNoMatchingErrors(t, pattern, errs)
		return
	}
	_, errs = ctx.PrepareBuildActions(config)
	if len(errs) > 0 {
		android.FailIfNoMatchingErrors(t, pattern, errs)
		return
	}

	t.Fatalf("missing expected error %q (0 errors are returned)", pattern)
}

func ensureListContains(t *testing.T, result []string, expected string) {
	t.Helper()
	if !android.InList(expected, result) {
		t.Errorf("%q is not found in %v", expected, result)
	}
}

func pathsToStrings(paths android.Paths) []string {
	var ret []string
	for _, p := range paths {
		ret = append(ret, p.String())
	}
	return ret
}

// Provides general test support.
type TestHelper struct {
	t *testing.T
}

func (h *TestHelper) AssertStringEquals(message string, expected string, actual string) {
	h.t.Helper()
	if actual != expected {
		h.t.Errorf("%s: expected %s, actual %s", message, expected, actual)
	}
}

func (h *TestHelper) AssertTrimmedStringEquals(message string, expected string, actual string) {
	h.t.Helper()
	h.AssertStringEquals(message, strings.TrimSpace(expected), strings.TrimSpace(actual))
}

// Encapsulates result of processing an SDK definition. Provides support for
// checking the state of the build structures.
type testSdkResult struct {
	TestHelper
	ctx    *android.TestContext
	config android.Config
}

// Analyse the sdk build rules to extract information about what it is doing.

// e.g. find the src/dest pairs from each cp command, the various zip files
// generated, etc.
func (r *testSdkResult) getSdkSnapshotBuildInfo(sdk *sdk) *snapshotBuildInfo {
	androidBpContents := strings.NewReplacer("\\n", "\n").Replace(sdk.GetAndroidBpContentsForTests())

	info := &snapshotBuildInfo{
		r:                 r,
		androidBpContents: androidBpContents,
	}

	buildParams := sdk.BuildParamsForTests()
	copyRules := &strings.Builder{}
	for _, bp := range buildParams {
		switch bp.Rule.String() {
		case android.Cp.String():
			// Get source relative to build directory.
			src := r.pathRelativeToBuildDir(bp.Input)
			// Get destination relative to the snapshot root
			dest := bp.Output.Rel()
			_, _ = fmt.Fprintf(copyRules, "%s -> %s\n", src, dest)
			info.snapshotContents = append(info.snapshotContents, dest)

		case repackageZip.String():
			// Add the destdir to the snapshot contents as that is effectively where
			// the content of the repackaged zip is copied.
			dest := bp.Args["destdir"]
			info.snapshotContents = append(info.snapshotContents, dest)

		case zipFiles.String():
			// This could be an intermediate zip file and not the actual output zip.
			// In that case this will be overridden when the rule to merge the zips
			// is processed.
			info.outputZip = r.pathRelativeToBuildDir(bp.Output)

		case mergeZips.String():
			// Copy the current outputZip to the intermediateZip.
			info.intermediateZip = info.outputZip
			mergeInput := r.pathRelativeToBuildDir(bp.Input)
			if info.intermediateZip != mergeInput {
				r.t.Errorf("Expected intermediate zip %s to be an input to merge zips but found %s instead",
					info.intermediateZip, mergeInput)
			}

			// Override output zip (which was actually the intermediate zip file) with the actual
			// output zip.
			info.outputZip = r.pathRelativeToBuildDir(bp.Output)

			// Save the zips to be merged into the intermediate zip.
			info.mergeZips = r.pathsRelativeToBuildDir(bp.Inputs)
		}
	}

	info.copyRules = copyRules.String()

	return info
}

func (r *testSdkResult) Module(name string, variant string) android.Module {
	return r.ctx.ModuleForTests(name, variant).Module()
}

func (r *testSdkResult) ModuleForTests(name string, variant string) android.TestingModule {
	return r.ctx.ModuleForTests(name, variant)
}

func (r *testSdkResult) pathRelativeToBuildDir(path android.Path) string {
	buildDir := filepath.Clean(r.config.BuildDir()) + "/"
	return strings.TrimPrefix(filepath.Clean(path.String()), buildDir)
}

func (r *testSdkResult) pathsRelativeToBuildDir(paths android.Paths) []string {
	var result []string
	for _, path := range paths {
		result = append(result, r.pathRelativeToBuildDir(path))
	}
	return result
}

// Check the snapshot build rules.
//
// Takes a list of functions which check different facets of the snapshot build rules.
// Allows each test to customize what is checked without duplicating lots of code
// or proliferating check methods of different flavors.
func (r *testSdkResult) CheckSnapshot(name string, variant string, dir string, checkers ...snapshotBuildInfoChecker) {
	r.t.Helper()

	sdk := r.Module(name, variant).(*sdk)

	snapshotBuildInfo := r.getSdkSnapshotBuildInfo(sdk)

	// Check state of the snapshot build.
	for _, checker := range checkers {
		checker(snapshotBuildInfo)
	}

	// Make sure that the generated zip file is in the correct place.
	actual := snapshotBuildInfo.outputZip
	if dir != "" {
		dir = filepath.Clean(dir) + "/"
	}
	r.AssertStringEquals("Snapshot zip file in wrong place",
		fmt.Sprintf(".intermediates/%s%s/%s/%s-current.zip", dir, name, variant, name), actual)

	// Populate a mock filesystem with the files that would have been copied by
	// the rules.
	fs := make(map[string][]byte)
	for _, dest := range snapshotBuildInfo.snapshotContents {
		fs[dest] = nil
	}

	// Process the generated bp file to make sure it is valid.
	testSdkWithFs(r.t, snapshotBuildInfo.androidBpContents, fs)
}

type snapshotBuildInfoChecker func(info *snapshotBuildInfo)

// Check that the snapshot's generated Android.bp is correct.
//
// Both the expected and actual string are both trimmed before comparing.
func checkAndroidBpContents(expected string) snapshotBuildInfoChecker {
	return func(info *snapshotBuildInfo) {
		info.r.t.Helper()
		info.r.AssertTrimmedStringEquals("Android.bp contents do not match", expected, info.androidBpContents)
	}
}

// Check that the snapshot's copy rules are correct.
//
// The copy rules are formatted as <src> -> <dest>, one per line and then compared
// to the supplied expected string. Both the expected and actual string are trimmed
// before comparing.
func checkAllCopyRules(expected string) snapshotBuildInfoChecker {
	return func(info *snapshotBuildInfo) {
		info.r.t.Helper()
		info.r.AssertTrimmedStringEquals("Incorrect copy rules", expected, info.copyRules)
	}
}

// Check that the specified path is in the list of zips to merge with the intermediate zip.
func checkMergeZip(expected string) snapshotBuildInfoChecker {
	return func(info *snapshotBuildInfo) {
		info.r.t.Helper()
		if info.intermediateZip == "" {
			info.r.t.Errorf("No intermediate zip file was created")
		}
		ensureListContains(info.r.t, info.mergeZips, expected)
	}
}

// Encapsulates information about the snapshot build structure in order to insulate tests from
// knowing too much about internal structures.
//
// All source/input paths are relative either the build directory. All dest/output paths are
// relative to the snapshot root directory.
type snapshotBuildInfo struct {
	r *testSdkResult

	// The contents of the generated Android.bp file
	androidBpContents string

	// The paths, relative to the snapshot root, of all files and directories copied into the
	// snapshot.
	snapshotContents []string

	// A formatted representation of the src/dest pairs, one pair per line, of the format
	// src -> dest
	copyRules string

	// The path to the intermediate zip, which is a zip created from the source files copied
	// into the snapshot directory and which will be merged with other zips to form the final output.
	// Is am empty string if there is no intermediate zip because there are no zips to merge in.
	intermediateZip string

	// The paths to the zips to merge into the output zip, does not include the intermediate
	// zip.
	mergeZips []string

	// The final output zip.
	outputZip string
}

var buildDir string

func setUp() {
	var err error
	buildDir, err = ioutil.TempDir("", "soong_sdk_test")
	if err != nil {
		panic(err)
	}
}

func tearDown() {
	_ = os.RemoveAll(buildDir)
}

func runTestWithBuildDir(m *testing.M) {
	run := func() int {
		setUp()
		defer tearDown()

		return m.Run()
	}

	os.Exit(run())
}

func SkipIfNotLinux(t *testing.T) {
	t.Helper()
	if android.BuildOs != android.Linux {
		t.Skipf("Skipping as sdk snapshot generation is only supported on %s not %s", android.Linux, android.BuildOs)
	}
}
