// Copyright 2017 Google Inc. All rights reserved.
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
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"android/soong/android"
)

var buildDir string

func setUp() {
	var err error
	buildDir, err = ioutil.TempDir("", "soong_cc_test")
	if err != nil {
		panic(err)
	}
}

func tearDown() {
	os.RemoveAll(buildDir)
}

func TestMain(m *testing.M) {
	run := func() int {
		setUp()
		defer tearDown()

		return m.Run()
	}

	os.Exit(run())
}

func testCcWithConfig(t *testing.T, bp string, config android.Config) *android.TestContext {
	t.Helper()
	return testCcWithConfigForOs(t, bp, config, android.Android)
}

func testCcWithConfigForOs(t *testing.T, bp string, config android.Config, os android.OsType) *android.TestContext {
	t.Helper()
	ctx := CreateTestContext(bp, nil, os)
	ctx.Register()

	_, errs := ctx.ParseFileList(".", []string{"Android.bp"})
	android.FailIfErrored(t, errs)
	_, errs = ctx.PrepareBuildActions(config)
	android.FailIfErrored(t, errs)

	return ctx
}

func testCc(t *testing.T, bp string) *android.TestContext {
	t.Helper()
	config := android.TestArchConfig(buildDir, nil)
	config.TestProductVariables.DeviceVndkVersion = StringPtr("current")
	config.TestProductVariables.Platform_vndk_version = StringPtr("VER")

	return testCcWithConfig(t, bp, config)
}

func testCcNoVndk(t *testing.T, bp string) *android.TestContext {
	t.Helper()
	config := android.TestArchConfig(buildDir, nil)
	config.TestProductVariables.Platform_vndk_version = StringPtr("VER")

	return testCcWithConfig(t, bp, config)
}

func testCcError(t *testing.T, pattern string, bp string) {
	t.Helper()
	config := android.TestArchConfig(buildDir, nil)
	config.TestProductVariables.DeviceVndkVersion = StringPtr("current")
	config.TestProductVariables.Platform_vndk_version = StringPtr("VER")

	ctx := CreateTestContext(bp, nil, android.Android)
	ctx.Register()

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

const (
	coreVariant     = "android_arm64_armv8-a_core_shared"
	vendorVariant   = "android_arm64_armv8-a_vendor.VER_shared"
	recoveryVariant = "android_arm64_armv8-a_recovery_shared"
)

func TestFuchsiaDeps(t *testing.T) {
	t.Helper()

	bp := `
		cc_library {
			name: "libTest",
			srcs: ["foo.c"],
			target: {
				fuchsia: {
					srcs: ["bar.c"],
				},
			},
		}`

	config := android.TestArchConfigFuchsia(buildDir, nil)
	ctx := testCcWithConfigForOs(t, bp, config, android.Fuchsia)

	rt := false
	fb := false

	ld := ctx.ModuleForTests("libTest", "fuchsia_arm64_shared").Rule("ld")
	implicits := ld.Implicits
	for _, lib := range implicits {
		if strings.Contains(lib.Rel(), "libcompiler_rt") {
			rt = true
		}

		if strings.Contains(lib.Rel(), "libbioniccompat") {
			fb = true
		}
	}

	if !rt || !fb {
		t.Errorf("fuchsia libs must link libcompiler_rt and libbioniccompat")
	}
}

func TestFuchsiaTargetDecl(t *testing.T) {
	t.Helper()

	bp := `
		cc_library {
			name: "libTest",
			srcs: ["foo.c"],
			target: {
				fuchsia: {
					srcs: ["bar.c"],
				},
			},
		}`

	config := android.TestArchConfigFuchsia(buildDir, nil)
	ctx := testCcWithConfigForOs(t, bp, config, android.Fuchsia)
	ld := ctx.ModuleForTests("libTest", "fuchsia_arm64_shared").Rule("ld")
	var objs []string
	for _, o := range ld.Inputs {
		objs = append(objs, o.Base())
	}
	if len(objs) != 2 || objs[0] != "foo.o" || objs[1] != "bar.o" {
		t.Errorf("inputs of libTest must be []string{\"foo.o\", \"bar.o\"}, but was %#v.", objs)
	}
}

func TestVendorSrc(t *testing.T) {
	ctx := testCc(t, `
		cc_library {
			name: "libTest",
			srcs: ["foo.c"],
			no_libcrt: true,
			nocrt: true,
			system_shared_libs: [],
			vendor_available: true,
			target: {
				vendor: {
					srcs: ["bar.c"],
				},
			},
		}
	`)

	ld := ctx.ModuleForTests("libTest", vendorVariant).Rule("ld")
	var objs []string
	for _, o := range ld.Inputs {
		objs = append(objs, o.Base())
	}
	if len(objs) != 2 || objs[0] != "foo.o" || objs[1] != "bar.o" {
		t.Errorf("inputs of libTest must be []string{\"foo.o\", \"bar.o\"}, but was %#v.", objs)
	}
}

func checkVndkModule(t *testing.T, ctx *android.TestContext, name, subDir string,
	isVndkSp bool, extends string) {

	t.Helper()

	mod := ctx.ModuleForTests(name, vendorVariant).Module().(*Module)
	if !mod.hasVendorVariant() {
		t.Errorf("%q must have vendor variant", name)
	}

	// Check library properties.
	lib, ok := mod.compiler.(*libraryDecorator)
	if !ok {
		t.Errorf("%q must have libraryDecorator", name)
	} else if lib.baseInstaller.subDir != subDir {
		t.Errorf("%q must use %q as subdir but it is using %q", name, subDir,
			lib.baseInstaller.subDir)
	}

	// Check VNDK properties.
	if mod.vndkdep == nil {
		t.Fatalf("%q must have `vndkdep`", name)
	}
	if !mod.isVndk() {
		t.Errorf("%q isVndk() must equal to true", name)
	}
	if mod.isVndkSp() != isVndkSp {
		t.Errorf("%q isVndkSp() must equal to %t", name, isVndkSp)
	}

	// Check VNDK extension properties.
	isVndkExt := extends != ""
	if mod.isVndkExt() != isVndkExt {
		t.Errorf("%q isVndkExt() must equal to %t", name, isVndkExt)
	}

	if actualExtends := mod.getVndkExtendsModuleName(); actualExtends != extends {
		t.Errorf("%q must extend from %q but get %q", name, extends, actualExtends)
	}
}

func checkVndkSnapshot(t *testing.T, ctx *android.TestContext, name, subDir, variant string) {
	vndkSnapshot := ctx.SingletonForTests("vndk-snapshot")

	snapshotPath := filepath.Join(subDir, name+".so")
	mod := ctx.ModuleForTests(name, variant).Module().(*Module)
	if !mod.outputFile.Valid() {
		t.Errorf("%q must have output\n", name)
		return
	}

	out := vndkSnapshot.Output(snapshotPath)
	if out.Input != mod.outputFile.Path() {
		t.Errorf("The input of VNDK snapshot must be %q, but %q", out.Input.String(), mod.outputFile.String())
	}
}

func TestVndk(t *testing.T) {
	config := android.TestArchConfig(buildDir, nil)
	config.TestProductVariables.DeviceVndkVersion = StringPtr("current")
	config.TestProductVariables.Platform_vndk_version = StringPtr("VER")

	ctx := testCcWithConfig(t, `
		cc_library {
			name: "libvndk",
			vendor_available: true,
			vndk: {
				enabled: true,
			},
			nocrt: true,
		}

		cc_library {
			name: "libvndk_private",
			vendor_available: false,
			vndk: {
				enabled: true,
			},
			nocrt: true,
		}

		cc_library {
			name: "libvndk_sp",
			vendor_available: true,
			vndk: {
				enabled: true,
				support_system_process: true,
			},
			nocrt: true,
		}

		cc_library {
			name: "libvndk_sp_private",
			vendor_available: false,
			vndk: {
				enabled: true,
				support_system_process: true,
			},
			nocrt: true,
		}
	`, config)

	checkVndkModule(t, ctx, "libvndk", "vndk-VER", false, "")
	checkVndkModule(t, ctx, "libvndk_private", "vndk-VER", false, "")
	checkVndkModule(t, ctx, "libvndk_sp", "vndk-sp-VER", true, "")
	checkVndkModule(t, ctx, "libvndk_sp_private", "vndk-sp-VER", true, "")

	// Check VNDK snapshot output.

	snapshotDir := "vndk-snapshot"
	snapshotVariantPath := filepath.Join(buildDir, snapshotDir, "arm64")

	vndkLibPath := filepath.Join(snapshotVariantPath, fmt.Sprintf("arch-%s-%s",
		"arm64", "armv8-a"))
	vndkLib2ndPath := filepath.Join(snapshotVariantPath, fmt.Sprintf("arch-%s-%s",
		"arm", "armv7-a-neon"))

	vndkCoreLibPath := filepath.Join(vndkLibPath, "shared", "vndk-core")
	vndkSpLibPath := filepath.Join(vndkLibPath, "shared", "vndk-sp")
	vndkCoreLib2ndPath := filepath.Join(vndkLib2ndPath, "shared", "vndk-core")
	vndkSpLib2ndPath := filepath.Join(vndkLib2ndPath, "shared", "vndk-sp")

	variant := "android_arm64_armv8-a_vendor.VER_shared"
	variant2nd := "android_arm_armv7-a-neon_vendor.VER_shared"

	checkVndkSnapshot(t, ctx, "libvndk", vndkCoreLibPath, variant)
	checkVndkSnapshot(t, ctx, "libvndk", vndkCoreLib2ndPath, variant2nd)
	checkVndkSnapshot(t, ctx, "libvndk_sp", vndkSpLibPath, variant)
	checkVndkSnapshot(t, ctx, "libvndk_sp", vndkSpLib2ndPath, variant2nd)
}

func TestVndkDepError(t *testing.T) {
	// Check whether an error is emitted when a VNDK lib depends on a system lib.
	testCcError(t, "dependency \".*\" of \".*\" missing variant", `
		cc_library {
			name: "libvndk",
			vendor_available: true,
			vndk: {
				enabled: true,
			},
			shared_libs: ["libfwk"],  // Cause error
			nocrt: true,
		}

		cc_library {
			name: "libfwk",
			nocrt: true,
		}
	`)

	// Check whether an error is emitted when a VNDK lib depends on a vendor lib.
	testCcError(t, "dependency \".*\" of \".*\" missing variant", `
		cc_library {
			name: "libvndk",
			vendor_available: true,
			vndk: {
				enabled: true,
			},
			shared_libs: ["libvendor"],  // Cause error
			nocrt: true,
		}

		cc_library {
			name: "libvendor",
			vendor: true,
			nocrt: true,
		}
	`)

	// Check whether an error is emitted when a VNDK-SP lib depends on a system lib.
	testCcError(t, "dependency \".*\" of \".*\" missing variant", `
		cc_library {
			name: "libvndk_sp",
			vendor_available: true,
			vndk: {
				enabled: true,
				support_system_process: true,
			},
			shared_libs: ["libfwk"],  // Cause error
			nocrt: true,
		}

		cc_library {
			name: "libfwk",
			nocrt: true,
		}
	`)

	// Check whether an error is emitted when a VNDK-SP lib depends on a vendor lib.
	testCcError(t, "dependency \".*\" of \".*\" missing variant", `
		cc_library {
			name: "libvndk_sp",
			vendor_available: true,
			vndk: {
				enabled: true,
				support_system_process: true,
			},
			shared_libs: ["libvendor"],  // Cause error
			nocrt: true,
		}

		cc_library {
			name: "libvendor",
			vendor: true,
			nocrt: true,
		}
	`)

	// Check whether an error is emitted when a VNDK-SP lib depends on a VNDK lib.
	testCcError(t, "module \".*\" variant \".*\": \\(.*\\) should not link to \".*\"", `
		cc_library {
			name: "libvndk_sp",
			vendor_available: true,
			vndk: {
				enabled: true,
				support_system_process: true,
			},
			shared_libs: ["libvndk"],  // Cause error
			nocrt: true,
		}

		cc_library {
			name: "libvndk",
			vendor_available: true,
			vndk: {
				enabled: true,
			},
			nocrt: true,
		}
	`)

	// Check whether an error is emitted when a VNDK lib depends on a non-VNDK lib.
	testCcError(t, "module \".*\" variant \".*\": \\(.*\\) should not link to \".*\"", `
		cc_library {
			name: "libvndk",
			vendor_available: true,
			vndk: {
				enabled: true,
			},
			shared_libs: ["libnonvndk"],
			nocrt: true,
		}

		cc_library {
			name: "libnonvndk",
			vendor_available: true,
			nocrt: true,
		}
	`)

	// Check whether an error is emitted when a VNDK-private lib depends on a non-VNDK lib.
	testCcError(t, "module \".*\" variant \".*\": \\(.*\\) should not link to \".*\"", `
		cc_library {
			name: "libvndkprivate",
			vendor_available: false,
			vndk: {
				enabled: true,
			},
			shared_libs: ["libnonvndk"],
			nocrt: true,
		}

		cc_library {
			name: "libnonvndk",
			vendor_available: true,
			nocrt: true,
		}
	`)

	// Check whether an error is emitted when a VNDK-sp lib depends on a non-VNDK lib.
	testCcError(t, "module \".*\" variant \".*\": \\(.*\\) should not link to \".*\"", `
		cc_library {
			name: "libvndksp",
			vendor_available: true,
			vndk: {
				enabled: true,
				support_system_process: true,
			},
			shared_libs: ["libnonvndk"],
			nocrt: true,
		}

		cc_library {
			name: "libnonvndk",
			vendor_available: true,
			nocrt: true,
		}
	`)

	// Check whether an error is emitted when a VNDK-sp-private lib depends on a non-VNDK lib.
	testCcError(t, "module \".*\" variant \".*\": \\(.*\\) should not link to \".*\"", `
		cc_library {
			name: "libvndkspprivate",
			vendor_available: false,
			vndk: {
				enabled: true,
				support_system_process: true,
			},
			shared_libs: ["libnonvndk"],
			nocrt: true,
		}

		cc_library {
			name: "libnonvndk",
			vendor_available: true,
			nocrt: true,
		}
	`)
}

func TestDoubleLoadbleDep(t *testing.T) {
	// okay to link : LLNDK -> double_loadable VNDK
	testCc(t, `
		cc_library {
			name: "libllndk",
			shared_libs: ["libdoubleloadable"],
		}

		llndk_library {
			name: "libllndk",
			symbol_file: "",
		}

		cc_library {
			name: "libdoubleloadable",
			vendor_available: true,
			vndk: {
				enabled: true,
			},
			double_loadable: true,
		}
	`)
	// okay to link : LLNDK -> VNDK-SP
	testCc(t, `
		cc_library {
			name: "libllndk",
			shared_libs: ["libvndksp"],
		}

		llndk_library {
			name: "libllndk",
			symbol_file: "",
		}

		cc_library {
			name: "libvndksp",
			vendor_available: true,
			vndk: {
				enabled: true,
				support_system_process: true,
			},
		}
	`)
	// okay to link : double_loadable -> double_loadable
	testCc(t, `
		cc_library {
			name: "libdoubleloadable1",
			shared_libs: ["libdoubleloadable2"],
			vendor_available: true,
			double_loadable: true,
		}

		cc_library {
			name: "libdoubleloadable2",
			vendor_available: true,
			double_loadable: true,
		}
	`)
	// okay to link : double_loadable VNDK -> double_loadable VNDK private
	testCc(t, `
		cc_library {
			name: "libdoubleloadable",
			vendor_available: true,
			vndk: {
				enabled: true,
			},
			double_loadable: true,
			shared_libs: ["libnondoubleloadable"],
		}

		cc_library {
			name: "libnondoubleloadable",
			vendor_available: false,
			vndk: {
				enabled: true,
			},
			double_loadable: true,
		}
	`)
	// okay to link : LLNDK -> core-only -> vendor_available & double_loadable
	testCc(t, `
		cc_library {
			name: "libllndk",
			shared_libs: ["libcoreonly"],
		}

		llndk_library {
			name: "libllndk",
			symbol_file: "",
		}

		cc_library {
			name: "libcoreonly",
			shared_libs: ["libvendoravailable"],
		}

		// indirect dependency of LLNDK
		cc_library {
			name: "libvendoravailable",
			vendor_available: true,
			double_loadable: true,
		}
	`)
}

func TestDoubleLoadableDepError(t *testing.T) {
	// Check whether an error is emitted when a LLNDK depends on a non-double_loadable VNDK lib.
	testCcError(t, "module \".*\" variant \".*\": link.* \".*\" which is not LL-NDK, VNDK-SP, .*double_loadable", `
		cc_library {
			name: "libllndk",
			shared_libs: ["libnondoubleloadable"],
		}

		llndk_library {
			name: "libllndk",
			symbol_file: "",
		}

		cc_library {
			name: "libnondoubleloadable",
			vendor_available: true,
			vndk: {
				enabled: true,
			},
		}
	`)

	// Check whether an error is emitted when a LLNDK depends on a non-double_loadable vendor_available lib.
	testCcError(t, "module \".*\" variant \".*\": link.* \".*\" which is not LL-NDK, VNDK-SP, .*double_loadable", `
		cc_library {
			name: "libllndk",
			no_libcrt: true,
			shared_libs: ["libnondoubleloadable"],
		}

		llndk_library {
			name: "libllndk",
			symbol_file: "",
		}

		cc_library {
			name: "libnondoubleloadable",
			vendor_available: true,
		}
	`)

	// Check whether an error is emitted when a double_loadable lib depends on a non-double_loadable vendor_available lib.
	testCcError(t, "module \".*\" variant \".*\": link.* \".*\" which is not LL-NDK, VNDK-SP, .*double_loadable", `
		cc_library {
			name: "libdoubleloadable",
			vendor_available: true,
			double_loadable: true,
			shared_libs: ["libnondoubleloadable"],
		}

		cc_library {
			name: "libnondoubleloadable",
			vendor_available: true,
		}
	`)

	// Check whether an error is emitted when a double_loadable lib depends on a non-double_loadable VNDK lib.
	testCcError(t, "module \".*\" variant \".*\": link.* \".*\" which is not LL-NDK, VNDK-SP, .*double_loadable", `
		cc_library {
			name: "libdoubleloadable",
			vendor_available: true,
			double_loadable: true,
			shared_libs: ["libnondoubleloadable"],
		}

		cc_library {
			name: "libnondoubleloadable",
			vendor_available: true,
			vndk: {
				enabled: true,
			},
		}
	`)

	// Check whether an error is emitted when a double_loadable VNDK depends on a non-double_loadable VNDK private lib.
	testCcError(t, "module \".*\" variant \".*\": link.* \".*\" which is not LL-NDK, VNDK-SP, .*double_loadable", `
		cc_library {
			name: "libdoubleloadable",
			vendor_available: true,
			vndk: {
				enabled: true,
			},
			double_loadable: true,
			shared_libs: ["libnondoubleloadable"],
		}

		cc_library {
			name: "libnondoubleloadable",
			vendor_available: false,
			vndk: {
				enabled: true,
			},
		}
	`)

	// Check whether an error is emitted when a LLNDK depends on a non-double_loadable indirectly.
	testCcError(t, "module \".*\" variant \".*\": link.* \".*\" which is not LL-NDK, VNDK-SP, .*double_loadable", `
		cc_library {
			name: "libllndk",
			shared_libs: ["libcoreonly"],
		}

		llndk_library {
			name: "libllndk",
			symbol_file: "",
		}

		cc_library {
			name: "libcoreonly",
			shared_libs: ["libvendoravailable"],
		}

		// indirect dependency of LLNDK
		cc_library {
			name: "libvendoravailable",
			vendor_available: true,
		}
	`)
}

func TestVndkMustNotBeProductSpecific(t *testing.T) {
	// Check whether an error is emitted when a vndk lib has 'product_specific: true'.
	testCcError(t, "product_specific must not be true when `vndk: {enabled: true}`", `
		cc_library {
			name: "libvndk",
			product_specific: true,  // Cause error
			vendor_available: true,
			vndk: {
				enabled: true,
			},
			nocrt: true,
		}
	`)
}

func TestVndkExt(t *testing.T) {
	// This test checks the VNDK-Ext properties.
	ctx := testCc(t, `
		cc_library {
			name: "libvndk",
			vendor_available: true,
			vndk: {
				enabled: true,
			},
			nocrt: true,
		}

		cc_library {
			name: "libvndk_ext",
			vendor: true,
			vndk: {
				enabled: true,
				extends: "libvndk",
			},
			nocrt: true,
		}
	`)

	checkVndkModule(t, ctx, "libvndk_ext", "vndk", false, "libvndk")
}

func TestVndkExtWithoutBoardVndkVersion(t *testing.T) {
	// This test checks the VNDK-Ext properties when BOARD_VNDK_VERSION is not set.
	ctx := testCcNoVndk(t, `
		cc_library {
			name: "libvndk",
			vendor_available: true,
			vndk: {
				enabled: true,
			},
			nocrt: true,
		}

		cc_library {
			name: "libvndk_ext",
			vendor: true,
			vndk: {
				enabled: true,
				extends: "libvndk",
			},
			nocrt: true,
		}
	`)

	// Ensures that the core variant of "libvndk_ext" can be found.
	mod := ctx.ModuleForTests("libvndk_ext", coreVariant).Module().(*Module)
	if extends := mod.getVndkExtendsModuleName(); extends != "libvndk" {
		t.Errorf("\"libvndk_ext\" must extend from \"libvndk\" but get %q", extends)
	}
}

func TestVndkExtError(t *testing.T) {
	// This test ensures an error is emitted in ill-formed vndk-ext definition.
	testCcError(t, "must set `vendor: true` to set `extends: \".*\"`", `
		cc_library {
			name: "libvndk",
			vendor_available: true,
			vndk: {
				enabled: true,
			},
			nocrt: true,
		}

		cc_library {
			name: "libvndk_ext",
			vndk: {
				enabled: true,
				extends: "libvndk",
			},
			nocrt: true,
		}
	`)

	testCcError(t, "must set `extends: \"\\.\\.\\.\"` to vndk extension", `
		cc_library {
			name: "libvndk",
			vendor_available: true,
			vndk: {
				enabled: true,
			},
			nocrt: true,
		}

		cc_library {
			name: "libvndk_ext",
			vendor: true,
			vndk: {
				enabled: true,
			},
			nocrt: true,
		}
	`)
}

func TestVndkExtInconsistentSupportSystemProcessError(t *testing.T) {
	// This test ensures an error is emitted for inconsistent support_system_process.
	testCcError(t, "module \".*\" with mismatched support_system_process", `
		cc_library {
			name: "libvndk",
			vendor_available: true,
			vndk: {
				enabled: true,
			},
			nocrt: true,
		}

		cc_library {
			name: "libvndk_sp_ext",
			vendor: true,
			vndk: {
				enabled: true,
				extends: "libvndk",
				support_system_process: true,
			},
			nocrt: true,
		}
	`)

	testCcError(t, "module \".*\" with mismatched support_system_process", `
		cc_library {
			name: "libvndk_sp",
			vendor_available: true,
			vndk: {
				enabled: true,
				support_system_process: true,
			},
			nocrt: true,
		}

		cc_library {
			name: "libvndk_ext",
			vendor: true,
			vndk: {
				enabled: true,
				extends: "libvndk_sp",
			},
			nocrt: true,
		}
	`)
}

func TestVndkExtVendorAvailableFalseError(t *testing.T) {
	// This test ensures an error is emitted when a VNDK-Ext library extends a VNDK library
	// with `vendor_available: false`.
	testCcError(t, "`extends` refers module \".*\" which does not have `vendor_available: true`", `
		cc_library {
			name: "libvndk",
			vendor_available: false,
			vndk: {
				enabled: true,
			},
			nocrt: true,
		}

		cc_library {
			name: "libvndk_ext",
			vendor: true,
			vndk: {
				enabled: true,
				extends: "libvndk",
			},
			nocrt: true,
		}
	`)
}

func TestVendorModuleUseVndkExt(t *testing.T) {
	// This test ensures a vendor module can depend on a VNDK-Ext library.
	testCc(t, `
		cc_library {
			name: "libvndk",
			vendor_available: true,
			vndk: {
				enabled: true,
			},
			nocrt: true,
		}

		cc_library {
			name: "libvndk_ext",
			vendor: true,
			vndk: {
				enabled: true,
				extends: "libvndk",
			},
			nocrt: true,
		}

		cc_library {

			name: "libvndk_sp",
			vendor_available: true,
			vndk: {
				enabled: true,
				support_system_process: true,
			},
			nocrt: true,
		}

		cc_library {
			name: "libvndk_sp_ext",
			vendor: true,
			vndk: {
				enabled: true,
				extends: "libvndk_sp",
				support_system_process: true,
			},
			nocrt: true,
		}

		cc_library {
			name: "libvendor",
			vendor: true,
			shared_libs: ["libvndk_ext", "libvndk_sp_ext"],
			nocrt: true,
		}
	`)
}

func TestVndkExtUseVendorLib(t *testing.T) {
	// This test ensures a VNDK-Ext library can depend on a vendor library.
	testCc(t, `
		cc_library {
			name: "libvndk",
			vendor_available: true,
			vndk: {
				enabled: true,
			},
			nocrt: true,
		}

		cc_library {
			name: "libvndk_ext",
			vendor: true,
			vndk: {
				enabled: true,
				extends: "libvndk",
			},
			shared_libs: ["libvendor"],
			nocrt: true,
		}

		cc_library {
			name: "libvendor",
			vendor: true,
			nocrt: true,
		}
	`)

	// This test ensures a VNDK-SP-Ext library can depend on a vendor library.
	testCc(t, `
		cc_library {
			name: "libvndk_sp",
			vendor_available: true,
			vndk: {
				enabled: true,
				support_system_process: true,
			},
			nocrt: true,
		}

		cc_library {
			name: "libvndk_sp_ext",
			vendor: true,
			vndk: {
				enabled: true,
				extends: "libvndk_sp",
				support_system_process: true,
			},
			shared_libs: ["libvendor"],  // Cause an error
			nocrt: true,
		}

		cc_library {
			name: "libvendor",
			vendor: true,
			nocrt: true,
		}
	`)
}

func TestVndkSpExtUseVndkError(t *testing.T) {
	// This test ensures an error is emitted if a VNDK-SP-Ext library depends on a VNDK
	// library.
	testCcError(t, "module \".*\" variant \".*\": \\(.*\\) should not link to \".*\"", `
		cc_library {
			name: "libvndk",
			vendor_available: true,
			vndk: {
				enabled: true,
			},
			nocrt: true,
		}

		cc_library {
			name: "libvndk_sp",
			vendor_available: true,
			vndk: {
				enabled: true,
				support_system_process: true,
			},
			nocrt: true,
		}

		cc_library {
			name: "libvndk_sp_ext",
			vendor: true,
			vndk: {
				enabled: true,
				extends: "libvndk_sp",
				support_system_process: true,
			},
			shared_libs: ["libvndk"],  // Cause an error
			nocrt: true,
		}
	`)

	// This test ensures an error is emitted if a VNDK-SP-Ext library depends on a VNDK-Ext
	// library.
	testCcError(t, "module \".*\" variant \".*\": \\(.*\\) should not link to \".*\"", `
		cc_library {
			name: "libvndk",
			vendor_available: true,
			vndk: {
				enabled: true,
			},
			nocrt: true,
		}

		cc_library {
			name: "libvndk_ext",
			vendor: true,
			vndk: {
				enabled: true,
				extends: "libvndk",
			},
			nocrt: true,
		}

		cc_library {
			name: "libvndk_sp",
			vendor_available: true,
			vndk: {
				enabled: true,
				support_system_process: true,
			},
			nocrt: true,
		}

		cc_library {
			name: "libvndk_sp_ext",
			vendor: true,
			vndk: {
				enabled: true,
				extends: "libvndk_sp",
				support_system_process: true,
			},
			shared_libs: ["libvndk_ext"],  // Cause an error
			nocrt: true,
		}
	`)
}

func TestVndkUseVndkExtError(t *testing.T) {
	// This test ensures an error is emitted if a VNDK/VNDK-SP library depends on a
	// VNDK-Ext/VNDK-SP-Ext library.
	testCcError(t, "dependency \".*\" of \".*\" missing variant", `
		cc_library {
			name: "libvndk",
			vendor_available: true,
			vndk: {
				enabled: true,
			},
			nocrt: true,
		}

		cc_library {
			name: "libvndk_ext",
			vendor: true,
			vndk: {
				enabled: true,
				extends: "libvndk",
			},
			nocrt: true,
		}

		cc_library {
			name: "libvndk2",
			vendor_available: true,
			vndk: {
				enabled: true,
			},
			shared_libs: ["libvndk_ext"],
			nocrt: true,
		}
	`)

	testCcError(t, "module \".*\" variant \".*\": \\(.*\\) should not link to \".*\"", `
		cc_library {
			name: "libvndk",
			vendor_available: true,
			vndk: {
				enabled: true,
			},
			nocrt: true,
		}

		cc_library {
			name: "libvndk_ext",
			vendor: true,
			vndk: {
				enabled: true,
				extends: "libvndk",
			},
			nocrt: true,
		}

		cc_library {
			name: "libvndk2",
			vendor_available: true,
			vndk: {
				enabled: true,
			},
			target: {
				vendor: {
					shared_libs: ["libvndk_ext"],
				},
			},
			nocrt: true,
		}
	`)

	testCcError(t, "dependency \".*\" of \".*\" missing variant", `
		cc_library {
			name: "libvndk_sp",
			vendor_available: true,
			vndk: {
				enabled: true,
				support_system_process: true,
			},
			nocrt: true,
		}

		cc_library {
			name: "libvndk_sp_ext",
			vendor: true,
			vndk: {
				enabled: true,
				extends: "libvndk_sp",
				support_system_process: true,
			},
			nocrt: true,
		}

		cc_library {
			name: "libvndk_sp_2",
			vendor_available: true,
			vndk: {
				enabled: true,
				support_system_process: true,
			},
			shared_libs: ["libvndk_sp_ext"],
			nocrt: true,
		}
	`)

	testCcError(t, "module \".*\" variant \".*\": \\(.*\\) should not link to \".*\"", `
		cc_library {
			name: "libvndk_sp",
			vendor_available: true,
			vndk: {
				enabled: true,
			},
			nocrt: true,
		}

		cc_library {
			name: "libvndk_sp_ext",
			vendor: true,
			vndk: {
				enabled: true,
				extends: "libvndk_sp",
			},
			nocrt: true,
		}

		cc_library {
			name: "libvndk_sp2",
			vendor_available: true,
			vndk: {
				enabled: true,
			},
			target: {
				vendor: {
					shared_libs: ["libvndk_sp_ext"],
				},
			},
			nocrt: true,
		}
	`)
}

func TestMakeLinkType(t *testing.T) {
	config := android.TestArchConfig(buildDir, nil)
	config.TestProductVariables.DeviceVndkVersion = StringPtr("current")
	config.TestProductVariables.Platform_vndk_version = StringPtr("VER")
	// native:vndk
	ctx := testCcWithConfig(t, `
	cc_library {
		name: "libvndk",
		vendor_available: true,
		vndk: {
			enabled: true,
		},
	}
	cc_library {
		name: "libvndksp",
		vendor_available: true,
		vndk: {
			enabled: true,
			support_system_process: true,
		},
	}
	cc_library {
		name: "libvndkprivate",
		vendor_available: false,
		vndk: {
			enabled: true,
		},
	}
	cc_library {
		name: "libvendor",
		vendor: true,
	}
	cc_library {
		name: "libvndkext",
		vendor: true,
		vndk: {
			enabled: true,
			extends: "libvndk",
		},
	}
	vndk_prebuilt_shared {
		name: "prevndk",
		version: "27",
		target_arch: "arm",
		binder32bit: true,
		vendor_available: true,
		vndk: {
			enabled: true,
		},
		arch: {
			arm: {
				srcs: ["liba.so"],
			},
		},
	}
	cc_library {
		name: "libllndk",
	}
	llndk_library {
		name: "libllndk",
		symbol_file: "",
	}
	cc_library {
		name: "libllndkprivate",
	}
	llndk_library {
		name: "libllndkprivate",
		vendor_available: false,
		symbol_file: "",
	}`, config)

	assertArrayString(t, *vndkCoreLibraries(config),
		[]string{"libvndk", "libvndkprivate"})
	assertArrayString(t, *vndkSpLibraries(config),
		[]string{"libc++", "libvndksp"})
	assertArrayString(t, *llndkLibraries(config),
		[]string{"libc", "libdl", "libllndk", "libllndkprivate", "libm"})
	assertArrayString(t, *vndkPrivateLibraries(config),
		[]string{"libllndkprivate", "libvndkprivate"})

	vendorVariant27 := "android_arm64_armv8-a_vendor.27_shared"

	tests := []struct {
		variant  string
		name     string
		expected string
	}{
		{vendorVariant, "libvndk", "native:vndk"},
		{vendorVariant, "libvndksp", "native:vndk"},
		{vendorVariant, "libvndkprivate", "native:vndk_private"},
		{vendorVariant, "libvendor", "native:vendor"},
		{vendorVariant, "libvndkext", "native:vendor"},
		{vendorVariant, "libllndk.llndk", "native:vndk"},
		{vendorVariant27, "prevndk.vndk.27.arm.binder32", "native:vndk"},
		{coreVariant, "libvndk", "native:platform"},
		{coreVariant, "libvndkprivate", "native:platform"},
		{coreVariant, "libllndk", "native:platform"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			module := ctx.ModuleForTests(test.name, test.variant).Module().(*Module)
			assertString(t, module.makeLinkType, test.expected)
		})
	}
}

var (
	str11 = "01234567891"
	str10 = str11[:10]
	str9  = str11[:9]
	str5  = str11[:5]
	str4  = str11[:4]
)

var splitListForSizeTestCases = []struct {
	in   []string
	out  [][]string
	size int
}{
	{
		in:   []string{str10},
		out:  [][]string{{str10}},
		size: 10,
	},
	{
		in:   []string{str9},
		out:  [][]string{{str9}},
		size: 10,
	},
	{
		in:   []string{str5},
		out:  [][]string{{str5}},
		size: 10,
	},
	{
		in:   []string{str11},
		out:  nil,
		size: 10,
	},
	{
		in:   []string{str10, str10},
		out:  [][]string{{str10}, {str10}},
		size: 10,
	},
	{
		in:   []string{str9, str10},
		out:  [][]string{{str9}, {str10}},
		size: 10,
	},
	{
		in:   []string{str10, str9},
		out:  [][]string{{str10}, {str9}},
		size: 10,
	},
	{
		in:   []string{str5, str4},
		out:  [][]string{{str5, str4}},
		size: 10,
	},
	{
		in:   []string{str5, str4, str5},
		out:  [][]string{{str5, str4}, {str5}},
		size: 10,
	},
	{
		in:   []string{str5, str4, str5, str4},
		out:  [][]string{{str5, str4}, {str5, str4}},
		size: 10,
	},
	{
		in:   []string{str5, str4, str5, str5},
		out:  [][]string{{str5, str4}, {str5}, {str5}},
		size: 10,
	},
	{
		in:   []string{str5, str5, str5, str4},
		out:  [][]string{{str5}, {str5}, {str5, str4}},
		size: 10,
	},
	{
		in:   []string{str9, str11},
		out:  nil,
		size: 10,
	},
	{
		in:   []string{str11, str9},
		out:  nil,
		size: 10,
	},
}

func TestSplitListForSize(t *testing.T) {
	for _, testCase := range splitListForSizeTestCases {
		out, _ := splitListForSize(android.PathsForTesting(testCase.in...), testCase.size)

		var outStrings [][]string

		if len(out) > 0 {
			outStrings = make([][]string, len(out))
			for i, o := range out {
				outStrings[i] = o.Strings()
			}
		}

		if !reflect.DeepEqual(outStrings, testCase.out) {
			t.Errorf("incorrect output:")
			t.Errorf("     input: %#v", testCase.in)
			t.Errorf("      size: %d", testCase.size)
			t.Errorf("  expected: %#v", testCase.out)
			t.Errorf("       got: %#v", outStrings)
		}
	}
}

var staticLinkDepOrderTestCases = []struct {
	// This is a string representation of a map[moduleName][]moduleDependency .
	// It models the dependencies declared in an Android.bp file.
	inStatic string

	// This is a string representation of a map[moduleName][]moduleDependency .
	// It models the dependencies declared in an Android.bp file.
	inShared string

	// allOrdered is a string representation of a map[moduleName][]moduleDependency .
	// The keys of allOrdered specify which modules we would like to check.
	// The values of allOrdered specify the expected result (of the transitive closure of all
	// dependencies) for each module to test
	allOrdered string

	// outOrdered is a string representation of a map[moduleName][]moduleDependency .
	// The keys of outOrdered specify which modules we would like to check.
	// The values of outOrdered specify the expected result (of the ordered linker command line)
	// for each module to test.
	outOrdered string
}{
	// Simple tests
	{
		inStatic:   "",
		outOrdered: "",
	},
	{
		inStatic:   "a:",
		outOrdered: "a:",
	},
	{
		inStatic:   "a:b; b:",
		outOrdered: "a:b; b:",
	},
	// Tests of reordering
	{
		// diamond example
		inStatic:   "a:d,b,c; b:d; c:d; d:",
		outOrdered: "a:b,c,d; b:d; c:d; d:",
	},
	{
		// somewhat real example
		inStatic:   "bsdiff_unittest:b,c,d,e,f,g,h,i; e:b",
		outOrdered: "bsdiff_unittest:c,d,e,b,f,g,h,i; e:b",
	},
	{
		// multiple reorderings
		inStatic:   "a:b,c,d,e; d:b; e:c",
		outOrdered: "a:d,b,e,c; d:b; e:c",
	},
	{
		// should reorder without adding new transitive dependencies
		inStatic:   "bin:lib2,lib1;             lib1:lib2,liboptional",
		allOrdered: "bin:lib1,lib2,liboptional; lib1:lib2,liboptional",
		outOrdered: "bin:lib1,lib2;             lib1:lib2,liboptional",
	},
	{
		// multiple levels of dependencies
		inStatic:   "a:b,c,d,e,f,g,h; f:b,c,d; b:c,d; c:d",
		allOrdered: "a:e,f,b,c,d,g,h; f:b,c,d; b:c,d; c:d",
		outOrdered: "a:e,f,b,c,d,g,h; f:b,c,d; b:c,d; c:d",
	},
	// shared dependencies
	{
		// Note that this test doesn't recurse, to minimize the amount of logic it tests.
		// So, we don't actually have to check that a shared dependency of c will change the order
		// of a library that depends statically on b and on c.  We only need to check that if c has
		// a shared dependency on b, that that shows up in allOrdered.
		inShared:   "c:b",
		allOrdered: "c:b",
		outOrdered: "c:",
	},
	{
		// This test doesn't actually include any shared dependencies but it's a reminder of what
		// the second phase of the above test would look like
		inStatic:   "a:b,c; c:b",
		allOrdered: "a:c,b; c:b",
		outOrdered: "a:c,b; c:b",
	},
	// tiebreakers for when two modules specifying different orderings and there is no dependency
	// to dictate an order
	{
		// if the tie is between two modules at the end of a's deps, then a's order wins
		inStatic:   "a1:b,c,d,e; a2:b,c,e,d; b:d,e; c:e,d",
		outOrdered: "a1:b,c,d,e; a2:b,c,e,d; b:d,e; c:e,d",
	},
	{
		// if the tie is between two modules at the start of a's deps, then c's order is used
		inStatic:   "a1:d,e,b1,c1; b1:d,e; c1:e,d;   a2:d,e,b2,c2; b2:d,e; c2:d,e",
		outOrdered: "a1:b1,c1,e,d; b1:d,e; c1:e,d;   a2:b2,c2,d,e; b2:d,e; c2:d,e",
	},
	// Tests involving duplicate dependencies
	{
		// simple duplicate
		inStatic:   "a:b,c,c,b",
		outOrdered: "a:c,b",
	},
	{
		// duplicates with reordering
		inStatic:   "a:b,c,d,c; c:b",
		outOrdered: "a:d,c,b",
	},
	// Tests to confirm the nonexistence of infinite loops.
	// These cases should never happen, so as long as the test terminates and the
	// result is deterministic then that should be fine.
	{
		inStatic:   "a:a",
		outOrdered: "a:a",
	},
	{
		inStatic:   "a:b;   b:c;   c:a",
		allOrdered: "a:b,c; b:c,a; c:a,b",
		outOrdered: "a:b;   b:c;   c:a",
	},
	{
		inStatic:   "a:b,c;   b:c,a;   c:a,b",
		allOrdered: "a:c,a,b; b:a,b,c; c:b,c,a",
		outOrdered: "a:c,b;   b:a,c;   c:b,a",
	},
}

// converts from a string like "a:b,c; d:e" to (["a","b"], {"a":["b","c"], "d":["e"]}, [{"a", "a.o"}, {"b", "b.o"}])
func parseModuleDeps(text string) (modulesInOrder []android.Path, allDeps map[android.Path][]android.Path) {
	// convert from "a:b,c; d:e" to "a:b,c;d:e"
	strippedText := strings.Replace(text, " ", "", -1)
	if len(strippedText) < 1 {
		return []android.Path{}, make(map[android.Path][]android.Path, 0)
	}
	allDeps = make(map[android.Path][]android.Path, 0)

	// convert from "a:b,c;d:e" to ["a:b,c", "d:e"]
	moduleTexts := strings.Split(strippedText, ";")

	outputForModuleName := func(moduleName string) android.Path {
		return android.PathForTesting(moduleName)
	}

	for _, moduleText := range moduleTexts {
		// convert from "a:b,c" to ["a", "b,c"]
		components := strings.Split(moduleText, ":")
		if len(components) != 2 {
			panic(fmt.Sprintf("illegal module dep string %q from larger string %q; must contain one ':', not %v", moduleText, text, len(components)-1))
		}
		moduleName := components[0]
		moduleOutput := outputForModuleName(moduleName)
		modulesInOrder = append(modulesInOrder, moduleOutput)

		depString := components[1]
		// convert from "b,c" to ["b", "c"]
		depNames := strings.Split(depString, ",")
		if len(depString) < 1 {
			depNames = []string{}
		}
		var deps []android.Path
		for _, depName := range depNames {
			deps = append(deps, outputForModuleName(depName))
		}
		allDeps[moduleOutput] = deps
	}
	return modulesInOrder, allDeps
}

func TestLinkReordering(t *testing.T) {
	for _, testCase := range staticLinkDepOrderTestCases {
		errs := []string{}

		// parse testcase
		_, givenTransitiveDeps := parseModuleDeps(testCase.inStatic)
		expectedModuleNames, expectedTransitiveDeps := parseModuleDeps(testCase.outOrdered)
		if testCase.allOrdered == "" {
			// allow the test case to skip specifying allOrdered
			testCase.allOrdered = testCase.outOrdered
		}
		_, expectedAllDeps := parseModuleDeps(testCase.allOrdered)
		_, givenAllSharedDeps := parseModuleDeps(testCase.inShared)

		// For each module whose post-reordered dependencies were specified, validate that
		// reordering the inputs produces the expected outputs.
		for _, moduleName := range expectedModuleNames {
			moduleDeps := givenTransitiveDeps[moduleName]
			givenSharedDeps := givenAllSharedDeps[moduleName]
			orderedAllDeps, orderedDeclaredDeps := orderDeps(moduleDeps, givenSharedDeps, givenTransitiveDeps)

			correctAllOrdered := expectedAllDeps[moduleName]
			if !reflect.DeepEqual(orderedAllDeps, correctAllOrdered) {
				errs = append(errs, fmt.Sprintf("orderDeps returned incorrect orderedAllDeps."+
					"\nin static:%q"+
					"\nin shared:%q"+
					"\nmodule:   %v"+
					"\nexpected: %s"+
					"\nactual:   %s",
					testCase.inStatic, testCase.inShared, moduleName, correctAllOrdered, orderedAllDeps))
			}

			correctOutputDeps := expectedTransitiveDeps[moduleName]
			if !reflect.DeepEqual(correctOutputDeps, orderedDeclaredDeps) {
				errs = append(errs, fmt.Sprintf("orderDeps returned incorrect orderedDeclaredDeps."+
					"\nin static:%q"+
					"\nin shared:%q"+
					"\nmodule:   %v"+
					"\nexpected: %s"+
					"\nactual:   %s",
					testCase.inStatic, testCase.inShared, moduleName, correctOutputDeps, orderedDeclaredDeps))
			}
		}

		if len(errs) > 0 {
			sort.Strings(errs)
			for _, err := range errs {
				t.Error(err)
			}
		}
	}
}

func getOutputPaths(ctx *android.TestContext, variant string, moduleNames []string) (paths android.Paths) {
	for _, moduleName := range moduleNames {
		module := ctx.ModuleForTests(moduleName, variant).Module().(*Module)
		output := module.outputFile.Path()
		paths = append(paths, output)
	}
	return paths
}

func TestStaticLibDepReordering(t *testing.T) {
	ctx := testCc(t, `
	cc_library {
		name: "a",
		static_libs: ["b", "c", "d"],
		stl: "none",
	}
	cc_library {
		name: "b",
		stl: "none",
	}
	cc_library {
		name: "c",
		static_libs: ["b"],
		stl: "none",
	}
	cc_library {
		name: "d",
		stl: "none",
	}

	`)

	variant := "android_arm64_armv8-a_core_static"
	moduleA := ctx.ModuleForTests("a", variant).Module().(*Module)
	actual := moduleA.depsInLinkOrder
	expected := getOutputPaths(ctx, variant, []string{"c", "b", "d"})

	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("staticDeps orderings were not propagated correctly"+
			"\nactual:   %v"+
			"\nexpected: %v",
			actual,
			expected,
		)
	}
}

func TestStaticLibDepReorderingWithShared(t *testing.T) {
	ctx := testCc(t, `
	cc_library {
		name: "a",
		static_libs: ["b", "c"],
		stl: "none",
	}
	cc_library {
		name: "b",
		stl: "none",
	}
	cc_library {
		name: "c",
		shared_libs: ["b"],
		stl: "none",
	}

	`)

	variant := "android_arm64_armv8-a_core_static"
	moduleA := ctx.ModuleForTests("a", variant).Module().(*Module)
	actual := moduleA.depsInLinkOrder
	expected := getOutputPaths(ctx, variant, []string{"c", "b"})

	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("staticDeps orderings did not account for shared libs"+
			"\nactual:   %v"+
			"\nexpected: %v",
			actual,
			expected,
		)
	}
}

func TestLlndkHeaders(t *testing.T) {
	ctx := testCc(t, `
	llndk_headers {
		name: "libllndk_headers",
		export_include_dirs: ["my_include"],
	}
	llndk_library {
		name: "libllndk",
		export_llndk_headers: ["libllndk_headers"],
	}
	cc_library {
		name: "libvendor",
		shared_libs: ["libllndk"],
		vendor: true,
		srcs: ["foo.c"],
		no_libcrt: true,
		nocrt: true,
	}
	`)

	// _static variant is used since _shared reuses *.o from the static variant
	cc := ctx.ModuleForTests("libvendor", "android_arm_armv7-a-neon_vendor.VER_static").Rule("cc")
	cflags := cc.Args["cFlags"]
	if !strings.Contains(cflags, "-Imy_include") {
		t.Errorf("cflags for libvendor must contain -Imy_include, but was %#v.", cflags)
	}
}

func checkRuntimeLibs(t *testing.T, expected []string, module *Module) {
	actual := module.Properties.AndroidMkRuntimeLibs
	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("incorrect runtime_libs for shared libs"+
			"\nactual:   %v"+
			"\nexpected: %v",
			actual,
			expected,
		)
	}
}

const runtimeLibAndroidBp = `
	cc_library {
		name: "libvendor_available1",
		vendor_available: true,
		no_libcrt : true,
		nocrt : true,
		system_shared_libs : [],
	}
	cc_library {
		name: "libvendor_available2",
		vendor_available: true,
		runtime_libs: ["libvendor_available1"],
		no_libcrt : true,
		nocrt : true,
		system_shared_libs : [],
	}
	cc_library {
		name: "libvendor_available3",
		vendor_available: true,
		runtime_libs: ["libvendor_available1"],
		target: {
			vendor: {
				exclude_runtime_libs: ["libvendor_available1"],
			}
		},
		no_libcrt : true,
		nocrt : true,
		system_shared_libs : [],
	}
	cc_library {
		name: "libcore",
		runtime_libs: ["libvendor_available1"],
		no_libcrt : true,
		nocrt : true,
		system_shared_libs : [],
	}
	cc_library {
		name: "libvendor1",
		vendor: true,
		no_libcrt : true,
		nocrt : true,
		system_shared_libs : [],
	}
	cc_library {
		name: "libvendor2",
		vendor: true,
		runtime_libs: ["libvendor_available1", "libvendor1"],
		no_libcrt : true,
		nocrt : true,
		system_shared_libs : [],
	}
`

func TestRuntimeLibs(t *testing.T) {
	ctx := testCc(t, runtimeLibAndroidBp)

	// runtime_libs for core variants use the module names without suffixes.
	variant := "android_arm64_armv8-a_core_shared"

	module := ctx.ModuleForTests("libvendor_available2", variant).Module().(*Module)
	checkRuntimeLibs(t, []string{"libvendor_available1"}, module)

	module = ctx.ModuleForTests("libcore", variant).Module().(*Module)
	checkRuntimeLibs(t, []string{"libvendor_available1"}, module)

	// runtime_libs for vendor variants have '.vendor' suffixes if the modules have both core
	// and vendor variants.
	variant = "android_arm64_armv8-a_vendor.VER_shared"

	module = ctx.ModuleForTests("libvendor_available2", variant).Module().(*Module)
	checkRuntimeLibs(t, []string{"libvendor_available1.vendor"}, module)

	module = ctx.ModuleForTests("libvendor2", variant).Module().(*Module)
	checkRuntimeLibs(t, []string{"libvendor_available1.vendor", "libvendor1"}, module)
}

func TestExcludeRuntimeLibs(t *testing.T) {
	ctx := testCc(t, runtimeLibAndroidBp)

	variant := "android_arm64_armv8-a_core_shared"
	module := ctx.ModuleForTests("libvendor_available3", variant).Module().(*Module)
	checkRuntimeLibs(t, []string{"libvendor_available1"}, module)

	variant = "android_arm64_armv8-a_vendor.VER_shared"
	module = ctx.ModuleForTests("libvendor_available3", variant).Module().(*Module)
	checkRuntimeLibs(t, nil, module)
}

func TestRuntimeLibsNoVndk(t *testing.T) {
	ctx := testCcNoVndk(t, runtimeLibAndroidBp)

	// If DeviceVndkVersion is not defined, then runtime_libs are copied as-is.

	variant := "android_arm64_armv8-a_core_shared"

	module := ctx.ModuleForTests("libvendor_available2", variant).Module().(*Module)
	checkRuntimeLibs(t, []string{"libvendor_available1"}, module)

	module = ctx.ModuleForTests("libvendor2", variant).Module().(*Module)
	checkRuntimeLibs(t, []string{"libvendor_available1", "libvendor1"}, module)
}

func checkStaticLibs(t *testing.T, expected []string, module *Module) {
	actual := module.Properties.AndroidMkStaticLibs
	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("incorrect static_libs"+
			"\nactual:   %v"+
			"\nexpected: %v",
			actual,
			expected,
		)
	}
}

const staticLibAndroidBp = `
	cc_library {
		name: "lib1",
	}
	cc_library {
		name: "lib2",
		static_libs: ["lib1"],
	}
`

func TestStaticLibDepExport(t *testing.T) {
	ctx := testCc(t, staticLibAndroidBp)

	// Check the shared version of lib2.
	variant := "android_arm64_armv8-a_core_shared"
	module := ctx.ModuleForTests("lib2", variant).Module().(*Module)
	checkStaticLibs(t, []string{"lib1", "libc++demangle", "libclang_rt.builtins-aarch64-android", "libatomic", "libgcc_stripped"}, module)

	// Check the static version of lib2.
	variant = "android_arm64_armv8-a_core_static"
	module = ctx.ModuleForTests("lib2", variant).Module().(*Module)
	// libc++_static is linked additionally.
	checkStaticLibs(t, []string{"lib1", "libc++_static", "libc++demangle", "libclang_rt.builtins-aarch64-android", "libatomic", "libgcc_stripped"}, module)
}

var compilerFlagsTestCases = []struct {
	in  string
	out bool
}{
	{
		in:  "a",
		out: false,
	},
	{
		in:  "-a",
		out: true,
	},
	{
		in:  "-Ipath/to/something",
		out: false,
	},
	{
		in:  "-isystempath/to/something",
		out: false,
	},
	{
		in:  "--coverage",
		out: false,
	},
	{
		in:  "-include a/b",
		out: true,
	},
	{
		in:  "-include a/b c/d",
		out: false,
	},
	{
		in:  "-DMACRO",
		out: true,
	},
	{
		in:  "-DMAC RO",
		out: false,
	},
	{
		in:  "-a -b",
		out: false,
	},
	{
		in:  "-DMACRO=definition",
		out: true,
	},
	{
		in:  "-DMACRO=defi nition",
		out: true, // TODO(jiyong): this should be false
	},
	{
		in:  "-DMACRO(x)=x + 1",
		out: true,
	},
	{
		in:  "-DMACRO=\"defi nition\"",
		out: true,
	},
}

type mockContext struct {
	BaseModuleContext
	result bool
}

func (ctx *mockContext) PropertyErrorf(property, format string, args ...interface{}) {
	// CheckBadCompilerFlags calls this function when the flag should be rejected
	ctx.result = false
}

func TestCompilerFlags(t *testing.T) {
	for _, testCase := range compilerFlagsTestCases {
		ctx := &mockContext{result: true}
		CheckBadCompilerFlags(ctx, "", []string{testCase.in})
		if ctx.result != testCase.out {
			t.Errorf("incorrect output:")
			t.Errorf("     input: %#v", testCase.in)
			t.Errorf("  expected: %#v", testCase.out)
			t.Errorf("       got: %#v", ctx.result)
		}
	}
}

func TestVendorPublicLibraries(t *testing.T) {
	ctx := testCc(t, `
	cc_library_headers {
		name: "libvendorpublic_headers",
		export_include_dirs: ["my_include"],
	}
	vendor_public_library {
		name: "libvendorpublic",
		symbol_file: "",
		export_public_headers: ["libvendorpublic_headers"],
	}
	cc_library {
		name: "libvendorpublic",
		srcs: ["foo.c"],
		vendor: true,
		no_libcrt: true,
		nocrt: true,
	}

	cc_library {
		name: "libsystem",
		shared_libs: ["libvendorpublic"],
		vendor: false,
		srcs: ["foo.c"],
		no_libcrt: true,
		nocrt: true,
	}
	cc_library {
		name: "libvendor",
		shared_libs: ["libvendorpublic"],
		vendor: true,
		srcs: ["foo.c"],
		no_libcrt: true,
		nocrt: true,
	}
	`)

	variant := "android_arm64_armv8-a_core_shared"

	// test if header search paths are correctly added
	// _static variant is used since _shared reuses *.o from the static variant
	cc := ctx.ModuleForTests("libsystem", strings.Replace(variant, "_shared", "_static", 1)).Rule("cc")
	cflags := cc.Args["cFlags"]
	if !strings.Contains(cflags, "-Imy_include") {
		t.Errorf("cflags for libsystem must contain -Imy_include, but was %#v.", cflags)
	}

	// test if libsystem is linked to the stub
	ld := ctx.ModuleForTests("libsystem", variant).Rule("ld")
	libflags := ld.Args["libFlags"]
	stubPaths := getOutputPaths(ctx, variant, []string{"libvendorpublic" + vendorPublicLibrarySuffix})
	if !strings.Contains(libflags, stubPaths[0].String()) {
		t.Errorf("libflags for libsystem must contain %#v, but was %#v", stubPaths[0], libflags)
	}

	// test if libvendor is linked to the real shared lib
	ld = ctx.ModuleForTests("libvendor", strings.Replace(variant, "_core", "_vendor.VER", 1)).Rule("ld")
	libflags = ld.Args["libFlags"]
	stubPaths = getOutputPaths(ctx, strings.Replace(variant, "_core", "_vendor.VER", 1), []string{"libvendorpublic"})
	if !strings.Contains(libflags, stubPaths[0].String()) {
		t.Errorf("libflags for libvendor must contain %#v, but was %#v", stubPaths[0], libflags)
	}

}

func TestRecovery(t *testing.T) {
	ctx := testCc(t, `
		cc_library_shared {
			name: "librecovery",
			recovery: true,
		}
		cc_library_shared {
			name: "librecovery32",
			recovery: true,
			compile_multilib:"32",
		}
		cc_library_shared {
			name: "libHalInRecovery",
			recovery_available: true,
			vendor: true,
		}
	`)

	variants := ctx.ModuleVariantsForTests("librecovery")
	const arm64 = "android_arm64_armv8-a_recovery_shared"
	if len(variants) != 1 || !android.InList(arm64, variants) {
		t.Errorf("variants of librecovery must be \"%s\" only, but was %#v", arm64, variants)
	}

	variants = ctx.ModuleVariantsForTests("librecovery32")
	if android.InList(arm64, variants) {
		t.Errorf("multilib was set to 32 for librecovery32, but its variants has %s.", arm64)
	}

	recoveryModule := ctx.ModuleForTests("libHalInRecovery", recoveryVariant).Module().(*Module)
	if !recoveryModule.Platform() {
		t.Errorf("recovery variant of libHalInRecovery must not specific to device, soc, or product")
	}
}

func TestVersionedStubs(t *testing.T) {
	ctx := testCc(t, `
		cc_library_shared {
			name: "libFoo",
			srcs: ["foo.c"],
			stubs: {
				symbol_file: "foo.map.txt",
				versions: ["1", "2", "3"],
			},
		}

		cc_library_shared {
			name: "libBar",
			srcs: ["bar.c"],
			shared_libs: ["libFoo#1"],
		}`)

	variants := ctx.ModuleVariantsForTests("libFoo")
	expectedVariants := []string{
		"android_arm64_armv8-a_core_shared",
		"android_arm64_armv8-a_core_shared_1",
		"android_arm64_armv8-a_core_shared_2",
		"android_arm64_armv8-a_core_shared_3",
		"android_arm_armv7-a-neon_core_shared",
		"android_arm_armv7-a-neon_core_shared_1",
		"android_arm_armv7-a-neon_core_shared_2",
		"android_arm_armv7-a-neon_core_shared_3",
	}
	variantsMismatch := false
	if len(variants) != len(expectedVariants) {
		variantsMismatch = true
	} else {
		for _, v := range expectedVariants {
			if !inList(v, variants) {
				variantsMismatch = false
			}
		}
	}
	if variantsMismatch {
		t.Errorf("variants of libFoo expected:\n")
		for _, v := range expectedVariants {
			t.Errorf("%q\n", v)
		}
		t.Errorf(", but got:\n")
		for _, v := range variants {
			t.Errorf("%q\n", v)
		}
	}

	libBarLinkRule := ctx.ModuleForTests("libBar", "android_arm64_armv8-a_core_shared").Rule("ld")
	libFlags := libBarLinkRule.Args["libFlags"]
	libFoo1StubPath := "libFoo/android_arm64_armv8-a_core_shared_1/libFoo.so"
	if !strings.Contains(libFlags, libFoo1StubPath) {
		t.Errorf("%q is not found in %q", libFoo1StubPath, libFlags)
	}

	libBarCompileRule := ctx.ModuleForTests("libBar", "android_arm64_armv8-a_core_shared").Rule("cc")
	cFlags := libBarCompileRule.Args["cFlags"]
	libFoo1VersioningMacro := "-D__LIBFOO_API__=1"
	if !strings.Contains(cFlags, libFoo1VersioningMacro) {
		t.Errorf("%q is not found in %q", libFoo1VersioningMacro, cFlags)
	}
}

func TestStaticExecutable(t *testing.T) {
	ctx := testCc(t, `
		cc_binary {
			name: "static_test",
			srcs: ["foo.c", "baz.o"],
			static_executable: true,
		}`)

	variant := "android_arm64_armv8-a_core"
	binModuleRule := ctx.ModuleForTests("static_test", variant).Rule("ld")
	libFlags := binModuleRule.Args["libFlags"]
	systemStaticLibs := []string{"libc.a", "libm.a", "libdl.a"}
	for _, lib := range systemStaticLibs {
		if !strings.Contains(libFlags, lib) {
			t.Errorf("Static lib %q was not found in %q", lib, libFlags)
		}
	}
	systemSharedLibs := []string{"libc.so", "libm.so", "libdl.so"}
	for _, lib := range systemSharedLibs {
		if strings.Contains(libFlags, lib) {
			t.Errorf("Shared lib %q was found in %q", lib, libFlags)
		}
	}
}

func TestStaticDepsOrderWithStubs(t *testing.T) {
	ctx := testCc(t, `
		cc_binary {
			name: "mybin",
			srcs: ["foo.c"],
			static_libs: ["libB"],
			static_executable: true,
			stl: "none",
		}

		cc_library {
			name: "libB",
			srcs: ["foo.c"],
			shared_libs: ["libC"],
			stl: "none",
		}

		cc_library {
			name: "libC",
			srcs: ["foo.c"],
			stl: "none",
			stubs: {
				versions: ["1"],
			},
		}`)

	mybin := ctx.ModuleForTests("mybin", "android_arm64_armv8-a_core").Module().(*Module)
	actual := mybin.depsInLinkOrder
	expected := getOutputPaths(ctx, "android_arm64_armv8-a_core_static", []string{"libB", "libC"})

	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("staticDeps orderings were not propagated correctly"+
			"\nactual:   %v"+
			"\nexpected: %v",
			actual,
			expected,
		)
	}
}

func TestErrorsIfAModuleDependsOnDisabled(t *testing.T) {
	testCcError(t, `module "libA" .* depends on disabled module "libB"`, `
		cc_library {
			name: "libA",
			srcs: ["foo.c"],
			shared_libs: ["libB"],
			stl: "none",
		}

		cc_library {
			name: "libB",
			srcs: ["foo.c"],
			enabled: false,
			stl: "none",
		}
	`)
}

// Simple smoke test for the cc_fuzz target that ensures the rule compiles
// correctly.
func TestFuzzTarget(t *testing.T) {
	ctx := testCc(t, `
		cc_fuzz {
			name: "fuzz_smoke_test",
			srcs: ["foo.c"],
		}`)

	variant := "android_arm64_armv8-a_core"
	ctx.ModuleForTests("fuzz_smoke_test", variant).Rule("cc")
}

func TestAidl(t *testing.T) {
}

func assertString(t *testing.T, got, expected string) {
	t.Helper()
	if got != expected {
		t.Errorf("expected %q got %q", expected, got)
	}
}

func assertArrayString(t *testing.T, got, expected []string) {
	t.Helper()
	if len(got) != len(expected) {
		t.Errorf("expected %d (%q) got (%d) %q", len(expected), expected, len(got), got)
		return
	}
	for i := range got {
		if got[i] != expected[i] {
			t.Errorf("expected %d-th %q (%q) got %q (%q)",
				i, expected[i], expected, got[i], got)
			return
		}
	}
}

func TestDefaults(t *testing.T) {
	ctx := testCc(t, `
		cc_defaults {
			name: "defaults",
			srcs: ["foo.c"],
			static: {
				srcs: ["bar.c"],
			},
			shared: {
				srcs: ["baz.c"],
			},
		}

		cc_library_static {
			name: "libstatic",
			defaults: ["defaults"],
		}

		cc_library_shared {
			name: "libshared",
			defaults: ["defaults"],
		}

		cc_library {
			name: "libboth",
			defaults: ["defaults"],
		}

		cc_binary {
			name: "binary",
			defaults: ["defaults"],
		}`)

	pathsToBase := func(paths android.Paths) []string {
		var ret []string
		for _, p := range paths {
			ret = append(ret, p.Base())
		}
		return ret
	}

	shared := ctx.ModuleForTests("libshared", "android_arm64_armv8-a_core_shared").Rule("ld")
	if g, w := pathsToBase(shared.Inputs), []string{"foo.o", "baz.o"}; !reflect.DeepEqual(w, g) {
		t.Errorf("libshared ld rule wanted %q, got %q", w, g)
	}
	bothShared := ctx.ModuleForTests("libboth", "android_arm64_armv8-a_core_shared").Rule("ld")
	if g, w := pathsToBase(bothShared.Inputs), []string{"foo.o", "baz.o"}; !reflect.DeepEqual(w, g) {
		t.Errorf("libboth ld rule wanted %q, got %q", w, g)
	}
	binary := ctx.ModuleForTests("binary", "android_arm64_armv8-a_core").Rule("ld")
	if g, w := pathsToBase(binary.Inputs), []string{"foo.o"}; !reflect.DeepEqual(w, g) {
		t.Errorf("binary ld rule wanted %q, got %q", w, g)
	}

	static := ctx.ModuleForTests("libstatic", "android_arm64_armv8-a_core_static").Rule("ar")
	if g, w := pathsToBase(static.Inputs), []string{"foo.o", "bar.o"}; !reflect.DeepEqual(w, g) {
		t.Errorf("libstatic ar rule wanted %q, got %q", w, g)
	}
	bothStatic := ctx.ModuleForTests("libboth", "android_arm64_armv8-a_core_static").Rule("ar")
	if g, w := pathsToBase(bothStatic.Inputs), []string{"foo.o", "bar.o"}; !reflect.DeepEqual(w, g) {
		t.Errorf("libboth ar rule wanted %q, got %q", w, g)
	}
}
