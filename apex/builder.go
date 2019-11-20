// Copyright (C) 2019 The Android Open Source Project
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

package apex

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"android/soong/android"
	"android/soong/java"

	"github.com/google/blueprint"
	"github.com/google/blueprint/proptools"
)

var (
	pctx = android.NewPackageContext("android/apex")
)

func init() {
	pctx.Import("android/soong/android")
	pctx.Import("android/soong/java")
	pctx.HostBinToolVariable("apexer", "apexer")
	// ART minimal builds (using the master-art manifest) do not have the "frameworks/base"
	// projects, and hence cannot built 'aapt2'. Use the SDK prebuilt instead.
	hostBinToolVariableWithPrebuilt := func(name, prebuiltDir, tool string) {
		pctx.VariableFunc(name, func(ctx android.PackageVarContext) string {
			if !ctx.Config().FrameworksBaseDirExists(ctx) {
				return filepath.Join(prebuiltDir, runtime.GOOS, "bin", tool)
			} else {
				return pctx.HostBinToolPath(ctx, tool).String()
			}
		})
	}
	hostBinToolVariableWithPrebuilt("aapt2", "prebuilts/sdk/tools", "aapt2")
	pctx.HostBinToolVariable("avbtool", "avbtool")
	pctx.HostBinToolVariable("e2fsdroid", "e2fsdroid")
	pctx.HostBinToolVariable("merge_zips", "merge_zips")
	pctx.HostBinToolVariable("mke2fs", "mke2fs")
	pctx.HostBinToolVariable("resize2fs", "resize2fs")
	pctx.HostBinToolVariable("sefcontext_compile", "sefcontext_compile")
	pctx.HostBinToolVariable("soong_zip", "soong_zip")
	pctx.HostBinToolVariable("zip2zip", "zip2zip")
	pctx.HostBinToolVariable("zipalign", "zipalign")
	pctx.HostBinToolVariable("jsonmodify", "jsonmodify")
	pctx.HostBinToolVariable("conv_apex_manifest", "conv_apex_manifest")
}

var (
	// Create a canned fs config file where all files and directories are
	// by default set to (uid/gid/mode) = (1000/1000/0644)
	// TODO(b/113082813) make this configurable using config.fs syntax
	generateFsConfig = pctx.StaticRule("generateFsConfig", blueprint.RuleParams{
		Command: `echo '/ 1000 1000 0755' > ${out} && ` +
			`echo ${ro_paths} | tr ' ' '\n' | awk '{print "/"$$1 " 1000 1000 0644"}' >> ${out} && ` +
			`echo ${exec_paths} | tr ' ' '\n' | awk '{print "/"$$1 " 0 2000 0755"}' >> ${out}`,
		Description: "fs_config ${out}",
	}, "ro_paths", "exec_paths")

	apexManifestRule = pctx.StaticRule("apexManifestRule", blueprint.RuleParams{
		Command: `rm -f $out && ${jsonmodify} $in ` +
			`-a provideNativeLibs ${provideNativeLibs} ` +
			`-a requireNativeLibs ${requireNativeLibs} ` +
			`${opt} ` +
			`-o $out`,
		CommandDeps: []string{"${jsonmodify}"},
		Description: "prepare ${out}",
	}, "provideNativeLibs", "requireNativeLibs", "opt")

	stripApexManifestRule = pctx.StaticRule("stripApexManifestRule", blueprint.RuleParams{
		Command:     `rm -f $out && ${conv_apex_manifest} strip $in -o $out`,
		CommandDeps: []string{"${conv_apex_manifest}"},
		Description: "strip ${in}=>${out}",
	})

	pbApexManifestRule = pctx.StaticRule("pbApexManifestRule", blueprint.RuleParams{
		Command:     `rm -f $out && ${conv_apex_manifest} proto $in -o $out`,
		CommandDeps: []string{"${conv_apex_manifest}"},
		Description: "convert ${in}=>${out}",
	})

	// TODO(b/113233103): make sure that file_contexts is sane, i.e., validate
	// against the binary policy using sefcontext_compiler -p <policy>.

	// TODO(b/114327326): automate the generation of file_contexts
	apexRule = pctx.StaticRule("apexRule", blueprint.RuleParams{
		Command: `rm -rf ${image_dir} && mkdir -p ${image_dir} && ` +
			`(. ${out}.copy_commands) && ` +
			`APEXER_TOOL_PATH=${tool_path} ` +
			`${apexer} --force --manifest ${manifest} ` +
			`--manifest_json ${manifest_json} --manifest_json_full ${manifest_json_full} ` +
			`--file_contexts ${file_contexts} ` +
			`--canned_fs_config ${canned_fs_config} ` +
			`--payload_type image ` +
			`--key ${key} ${opt_flags} ${image_dir} ${out} `,
		CommandDeps: []string{"${apexer}", "${avbtool}", "${e2fsdroid}", "${merge_zips}",
			"${mke2fs}", "${resize2fs}", "${sefcontext_compile}",
			"${soong_zip}", "${zipalign}", "${aapt2}", "prebuilts/sdk/current/public/android.jar"},
		Rspfile:        "${out}.copy_commands",
		RspfileContent: "${copy_commands}",
		Description:    "APEX ${image_dir} => ${out}",
	}, "tool_path", "image_dir", "copy_commands", "file_contexts", "canned_fs_config", "key", "opt_flags",
		"manifest", "manifest_json", "manifest_json_full",
	)

	zipApexRule = pctx.StaticRule("zipApexRule", blueprint.RuleParams{
		Command: `rm -rf ${image_dir} && mkdir -p ${image_dir} && ` +
			`(. ${out}.copy_commands) && ` +
			`APEXER_TOOL_PATH=${tool_path} ` +
			`${apexer} --force --manifest ${manifest} --manifest_json_full ${manifest_json_full} ` +
			`--payload_type zip ` +
			`${image_dir} ${out} `,
		CommandDeps:    []string{"${apexer}", "${merge_zips}", "${soong_zip}", "${zipalign}", "${aapt2}"},
		Rspfile:        "${out}.copy_commands",
		RspfileContent: "${copy_commands}",
		Description:    "ZipAPEX ${image_dir} => ${out}",
	}, "tool_path", "image_dir", "copy_commands", "manifest", "manifest_json_full")

	apexProtoConvertRule = pctx.AndroidStaticRule("apexProtoConvertRule",
		blueprint.RuleParams{
			Command:     `${aapt2} convert --output-format proto $in -o $out`,
			CommandDeps: []string{"${aapt2}"},
		})

	apexBundleRule = pctx.StaticRule("apexBundleRule", blueprint.RuleParams{
		Command: `${zip2zip} -i $in -o $out ` +
			`apex_payload.img:apex/${abi}.img ` +
			`apex_manifest.json:root/apex_manifest.json ` +
			`AndroidManifest.xml:manifest/AndroidManifest.xml ` +
			`assets/NOTICE.html.gz:assets/NOTICE.html.gz`,
		CommandDeps: []string{"${zip2zip}"},
		Description: "app bundle",
	}, "abi")

	emitApexContentRule = pctx.StaticRule("emitApexContentRule", blueprint.RuleParams{
		Command:        `rm -f ${out} && touch ${out} && (. ${out}.emit_commands)`,
		Rspfile:        "${out}.emit_commands",
		RspfileContent: "${emit_commands}",
		Description:    "Emit APEX image content",
	}, "emit_commands")

	diffApexContentRule = pctx.StaticRule("diffApexContentRule", blueprint.RuleParams{
		Command: `diff --unchanged-group-format='' \` +
			`--changed-group-format='%<' \` +
			`${image_content_file} ${whitelisted_files_file} || (` +
			`echo -e "New unexpected files were added to ${apex_module_name}." ` +
			` "To fix the build run following command:" && ` +
			`echo "system/apex/tools/update_whitelist.sh ${whitelisted_files_file} ${image_content_file}" && ` +
			`exit 1)`,
		Description: "Diff ${image_content_file} and ${whitelisted_files_file}",
	}, "image_content_file", "whitelisted_files_file", "apex_module_name")
)

func (a *apexBundle) buildManifest(ctx android.ModuleContext, provideNativeLibs, requireNativeLibs []string) {
	manifestSrc := android.PathForModuleSrc(ctx, proptools.StringDefault(a.properties.Manifest, "apex_manifest.json"))

	a.manifestJsonFullOut = android.PathForModuleOut(ctx, "apex_manifest_full.json")

	// put dependency({provide|require}NativeLibs) in apex_manifest.json
	provideNativeLibs = android.SortedUniqueStrings(provideNativeLibs)
	requireNativeLibs = android.SortedUniqueStrings(android.RemoveListFromList(requireNativeLibs, provideNativeLibs))

	// apex name can be overridden
	optCommands := []string{}
	if a.properties.Apex_name != nil {
		optCommands = append(optCommands, "-v name "+*a.properties.Apex_name)
	}

	ctx.Build(pctx, android.BuildParams{
		Rule:   apexManifestRule,
		Input:  manifestSrc,
		Output: a.manifestJsonFullOut,
		Args: map[string]string{
			"provideNativeLibs": strings.Join(provideNativeLibs, " "),
			"requireNativeLibs": strings.Join(requireNativeLibs, " "),
			"opt":               strings.Join(optCommands, " "),
		},
	})

	// b/143654022 Q apexd can't understand newly added keys in apex_manifest.json
	// prepare stripped-down version so that APEX modules built from R+ can be installed to Q
	a.manifestJsonOut = android.PathForModuleOut(ctx, "apex_manifest.json")
	ctx.Build(pctx, android.BuildParams{
		Rule:   stripApexManifestRule,
		Input:  a.manifestJsonFullOut,
		Output: a.manifestJsonOut,
	})

	// from R+, protobuf binary format (.pb) is the standard format for apex_manifest
	a.manifestPbOut = android.PathForModuleOut(ctx, "apex_manifest.pb")
	ctx.Build(pctx, android.BuildParams{
		Rule:   pbApexManifestRule,
		Input:  a.manifestJsonFullOut,
		Output: a.manifestPbOut,
	})
}

func (a *apexBundle) buildNoticeFile(ctx android.ModuleContext, apexFileName string) android.OptionalPath {
	noticeFiles := []android.Path{}
	for _, f := range a.filesInfo {
		if f.module != nil {
			notice := f.module.NoticeFile()
			if notice.Valid() {
				noticeFiles = append(noticeFiles, notice.Path())
			}
		}
	}
	// append the notice file specified in the apex module itself
	if a.NoticeFile().Valid() {
		noticeFiles = append(noticeFiles, a.NoticeFile().Path())
	}

	if len(noticeFiles) == 0 {
		return android.OptionalPath{}
	}

	return android.BuildNoticeOutput(ctx, a.installDir, apexFileName, android.FirstUniquePaths(noticeFiles)).HtmlGzOutput
}

func (a *apexBundle) buildUnflattenedApex(ctx android.ModuleContext) {
	var abis []string
	for _, target := range ctx.MultiTargets() {
		if len(target.Arch.Abi) > 0 {
			abis = append(abis, target.Arch.Abi[0])
		}
	}

	abis = android.FirstUniqueStrings(abis)

	apexType := a.properties.ApexType
	suffix := apexType.suffix()
	unsignedOutputFile := android.PathForModuleOut(ctx, ctx.ModuleName()+suffix+".unsigned")

	filesToCopy := []android.Path{}
	for _, f := range a.filesInfo {
		filesToCopy = append(filesToCopy, f.builtFile)
	}

	copyCommands := []string{}
	emitCommands := []string{}
	imageContentFile := android.PathForModuleOut(ctx, ctx.ModuleName()+"-content.txt")
	emitCommands = append(emitCommands, "echo ./apex_manifest.json >> "+imageContentFile.String())
	for i, src := range filesToCopy {
		dest := filepath.Join(a.filesInfo[i].installDir, src.Base())
		emitCommands = append(emitCommands, "echo './"+dest+"' >> "+imageContentFile.String())
		dest_path := filepath.Join(android.PathForModuleOut(ctx, "image"+suffix).String(), dest)
		copyCommands = append(copyCommands, "mkdir -p "+filepath.Dir(dest_path))
		copyCommands = append(copyCommands, "cp "+src.String()+" "+dest_path)
		for _, sym := range a.filesInfo[i].symlinks {
			symlinkDest := filepath.Join(filepath.Dir(dest_path), sym)
			copyCommands = append(copyCommands, "ln -s "+filepath.Base(dest)+" "+symlinkDest)
		}
	}
	emitCommands = append(emitCommands, "sort -o "+imageContentFile.String()+" "+imageContentFile.String())

	implicitInputs := append(android.Paths(nil), filesToCopy...)
	implicitInputs = append(implicitInputs, a.manifestPbOut, a.manifestJsonFullOut, a.manifestJsonOut)

	if a.properties.Whitelisted_files != nil {
		ctx.Build(pctx, android.BuildParams{
			Rule:        emitApexContentRule,
			Implicits:   implicitInputs,
			Output:      imageContentFile,
			Description: "emit apex image content",
			Args: map[string]string{
				"emit_commands": strings.Join(emitCommands, " && "),
			},
		})
		implicitInputs = append(implicitInputs, imageContentFile)
		whitelistedFilesFile := android.PathForModuleSrc(ctx, proptools.String(a.properties.Whitelisted_files))

		phonyOutput := android.PathForModuleOut(ctx, ctx.ModuleName()+"-diff-phony-output")
		ctx.Build(pctx, android.BuildParams{
			Rule:        diffApexContentRule,
			Implicits:   implicitInputs,
			Output:      phonyOutput,
			Description: "diff apex image content",
			Args: map[string]string{
				"whitelisted_files_file": whitelistedFilesFile.String(),
				"image_content_file":     imageContentFile.String(),
				"apex_module_name":       ctx.ModuleName(),
			},
		})

		implicitInputs = append(implicitInputs, phonyOutput)
	}

	outHostBinDir := android.PathForOutput(ctx, "host", ctx.Config().PrebuiltOS(), "bin").String()
	prebuiltSdkToolsBinDir := filepath.Join("prebuilts", "sdk", "tools", runtime.GOOS, "bin")

	if apexType == imageApex {
		// files and dirs that will be created in APEX
		var readOnlyPaths = []string{"apex_manifest.json", "apex_manifest.pb"}
		var executablePaths []string // this also includes dirs
		for _, f := range a.filesInfo {
			pathInApex := filepath.Join(f.installDir, f.builtFile.Base())
			if f.installDir == "bin" || strings.HasPrefix(f.installDir, "bin/") {
				executablePaths = append(executablePaths, pathInApex)
				for _, s := range f.symlinks {
					executablePaths = append(executablePaths, filepath.Join(f.installDir, s))
				}
			} else {
				readOnlyPaths = append(readOnlyPaths, pathInApex)
			}
			dir := f.installDir
			for !android.InList(dir, executablePaths) && dir != "" {
				executablePaths = append(executablePaths, dir)
				dir, _ = filepath.Split(dir) // move up to the parent
				if len(dir) > 0 {
					// remove trailing slash
					dir = dir[:len(dir)-1]
				}
			}
		}
		sort.Strings(readOnlyPaths)
		sort.Strings(executablePaths)
		cannedFsConfig := android.PathForModuleOut(ctx, "canned_fs_config")
		ctx.Build(pctx, android.BuildParams{
			Rule:        generateFsConfig,
			Output:      cannedFsConfig,
			Description: "generate fs config",
			Args: map[string]string{
				"ro_paths":   strings.Join(readOnlyPaths, " "),
				"exec_paths": strings.Join(executablePaths, " "),
			},
		})

		fcName := proptools.StringDefault(a.properties.File_contexts, ctx.ModuleName())
		fileContextsPath := "system/sepolicy/apex/" + fcName + "-file_contexts"
		fileContextsOptionalPath := android.ExistentPathForSource(ctx, fileContextsPath)
		if !fileContextsOptionalPath.Valid() {
			ctx.ModuleErrorf("Cannot find file_contexts file: %q", fileContextsPath)
			return
		}
		fileContexts := fileContextsOptionalPath.Path()

		optFlags := []string{}

		// Additional implicit inputs.
		implicitInputs = append(implicitInputs, cannedFsConfig, fileContexts, a.private_key_file, a.public_key_file)
		optFlags = append(optFlags, "--pubkey "+a.public_key_file.String())

		manifestPackageName, overridden := ctx.DeviceConfig().OverrideManifestPackageNameFor(ctx.ModuleName())
		if overridden {
			optFlags = append(optFlags, "--override_apk_package_name "+manifestPackageName)
		}

		if a.properties.AndroidManifest != nil {
			androidManifestFile := android.PathForModuleSrc(ctx, proptools.String(a.properties.AndroidManifest))
			implicitInputs = append(implicitInputs, androidManifestFile)
			optFlags = append(optFlags, "--android_manifest "+androidManifestFile.String())
		}

		targetSdkVersion := ctx.Config().DefaultAppTargetSdk()
		if targetSdkVersion == ctx.Config().PlatformSdkCodename() &&
			ctx.Config().UnbundledBuild() &&
			!ctx.Config().UnbundledBuildUsePrebuiltSdks() &&
			ctx.Config().IsEnvTrue("UNBUNDLED_BUILD_TARGET_SDK_WITH_API_FINGERPRINT") {
			apiFingerprint := java.ApiFingerprintPath(ctx)
			targetSdkVersion += fmt.Sprintf(".$$(cat %s)", apiFingerprint.String())
			implicitInputs = append(implicitInputs, apiFingerprint)
		}
		optFlags = append(optFlags, "--target_sdk_version "+targetSdkVersion)

		noticeFile := a.buildNoticeFile(ctx, ctx.ModuleName()+suffix)
		if noticeFile.Valid() {
			// If there's a NOTICE file, embed it as an asset file in the APEX.
			implicitInputs = append(implicitInputs, noticeFile.Path())
			optFlags = append(optFlags, "--assets_dir "+filepath.Dir(noticeFile.String()))
		}

		if !ctx.Config().UnbundledBuild() && a.installable() {
			// Apexes which are supposed to be installed in builtin dirs(/system, etc)
			// don't need hashtree for activation. Therefore, by removing hashtree from
			// apex bundle (filesystem image in it, to be specific), we can save storage.
			optFlags = append(optFlags, "--no_hashtree")
		}

		if a.properties.Apex_name != nil {
			// If apex_name is set, apexer can skip checking if key name matches with apex name.
			// Note that apex_manifest is also mended.
			optFlags = append(optFlags, "--do_not_check_keyname")
		}

		ctx.Build(pctx, android.BuildParams{
			Rule:        apexRule,
			Implicits:   implicitInputs,
			Output:      unsignedOutputFile,
			Description: "apex (" + apexType.name() + ")",
			Args: map[string]string{
				"tool_path":          outHostBinDir + ":" + prebuiltSdkToolsBinDir,
				"image_dir":          android.PathForModuleOut(ctx, "image"+suffix).String(),
				"copy_commands":      strings.Join(copyCommands, " && "),
				"manifest_json_full": a.manifestJsonFullOut.String(),
				"manifest_json":      a.manifestJsonOut.String(),
				"manifest":           a.manifestPbOut.String(),
				"file_contexts":      fileContexts.String(),
				"canned_fs_config":   cannedFsConfig.String(),
				"key":                a.private_key_file.String(),
				"opt_flags":          strings.Join(optFlags, " "),
			},
		})

		apexProtoFile := android.PathForModuleOut(ctx, ctx.ModuleName()+".pb"+suffix)
		bundleModuleFile := android.PathForModuleOut(ctx, ctx.ModuleName()+suffix+"-base.zip")
		a.bundleModuleFile = bundleModuleFile

		ctx.Build(pctx, android.BuildParams{
			Rule:        apexProtoConvertRule,
			Input:       unsignedOutputFile,
			Output:      apexProtoFile,
			Description: "apex proto convert",
		})

		ctx.Build(pctx, android.BuildParams{
			Rule:        apexBundleRule,
			Input:       apexProtoFile,
			Output:      a.bundleModuleFile,
			Description: "apex bundle module",
			Args: map[string]string{
				"abi": strings.Join(abis, "."),
			},
		})
	} else {
		ctx.Build(pctx, android.BuildParams{
			Rule:        zipApexRule,
			Implicits:   implicitInputs,
			Output:      unsignedOutputFile,
			Description: "apex (" + apexType.name() + ")",
			Args: map[string]string{
				"tool_path":          outHostBinDir + ":" + prebuiltSdkToolsBinDir,
				"image_dir":          android.PathForModuleOut(ctx, "image"+suffix).String(),
				"copy_commands":      strings.Join(copyCommands, " && "),
				"manifest":           a.manifestPbOut.String(),
				"manifest_json_full": a.manifestJsonFullOut.String(),
			},
		})
	}

	a.outputFile = android.PathForModuleOut(ctx, ctx.ModuleName()+suffix)
	ctx.Build(pctx, android.BuildParams{
		Rule:        java.Signapk,
		Description: "signapk",
		Output:      a.outputFile,
		Input:       unsignedOutputFile,
		Implicits: []android.Path{
			a.container_certificate_file,
			a.container_private_key_file,
		},
		Args: map[string]string{
			"certificates": a.container_certificate_file.String() + " " + a.container_private_key_file.String(),
			"flags":        "-a 4096", //alignment
		},
	})

	// Install to $OUT/soong/{target,host}/.../apex
	if a.installable() {
		ctx.InstallFile(a.installDir, ctx.ModuleName()+suffix, a.outputFile)
	}
	a.buildFilesInfo(ctx)
}

func (a *apexBundle) buildFlattenedApex(ctx android.ModuleContext) {
	// Temporarily wrap the original `ctx` into a `flattenedApexContext` to have it
	// reply true to `InstallBypassMake()` (thus making the call
	// `android.PathForModuleInstall` below use `android.pathForInstallInMakeDir`
	// instead of `android.PathForOutput`) to return the correct path to the flattened
	// APEX (as its contents is installed by Make, not Soong).
	factx := flattenedApexContext{ctx}
	apexName := proptools.StringDefault(a.properties.Apex_name, ctx.ModuleName())
	a.outputFile = android.PathForModuleInstall(&factx, "apex", apexName)

	a.buildFilesInfo(ctx)
}

func (a *apexBundle) setCertificateAndPrivateKey(ctx android.ModuleContext) {
	cert := String(a.properties.Certificate)
	if cert != "" && android.SrcIsModule(cert) == "" {
		defaultDir := ctx.Config().DefaultAppCertificateDir(ctx)
		a.container_certificate_file = defaultDir.Join(ctx, cert+".x509.pem")
		a.container_private_key_file = defaultDir.Join(ctx, cert+".pk8")
	} else if cert == "" {
		pem, key := ctx.Config().DefaultAppCertificate(ctx)
		a.container_certificate_file = pem
		a.container_private_key_file = key
	}
}

func (a *apexBundle) buildFilesInfo(ctx android.ModuleContext) {
	if a.installable() {
		// For flattened APEX, do nothing but make sure that apex_manifest.json and apex_pubkey are also copied along
		// with other ordinary files.
		a.filesInfo = append(a.filesInfo, apexFile{a.manifestJsonOut, "apex_manifest.json." + ctx.ModuleName() + a.suffix, ".", etc, nil, nil})
		a.filesInfo = append(a.filesInfo, apexFile{a.manifestPbOut, "apex_manifest.pb." + ctx.ModuleName() + a.suffix, ".", etc, nil, nil})

		// rename to apex_pubkey
		copiedPubkey := android.PathForModuleOut(ctx, "apex_pubkey")
		ctx.Build(pctx, android.BuildParams{
			Rule:   android.Cp,
			Input:  a.public_key_file,
			Output: copiedPubkey,
		})
		a.filesInfo = append(a.filesInfo, apexFile{copiedPubkey, "apex_pubkey." + ctx.ModuleName() + a.suffix, ".", etc, nil, nil})

		if a.properties.ApexType == flattenedApex {
			apexName := proptools.StringDefault(a.properties.Apex_name, ctx.ModuleName())
			for _, fi := range a.filesInfo {
				dir := filepath.Join("apex", apexName, fi.installDir)
				target := ctx.InstallFile(android.PathForModuleInstall(ctx, dir), fi.builtFile.Base(), fi.builtFile)
				for _, sym := range fi.symlinks {
					ctx.InstallSymlink(android.PathForModuleInstall(ctx, dir), sym, target)
				}
			}
		}
	}
}
