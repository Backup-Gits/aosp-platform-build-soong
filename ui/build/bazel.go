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

package build

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"android/soong/shared"
	"android/soong/ui/metrics"
)

func getBazelInfo(ctx Context, config Config, bazelExecutable string, query string) string {
	infoCmd := Command(ctx, config, "bazel", bazelExecutable)

	if extraStartupArgs, ok := infoCmd.Environment.Get("BAZEL_STARTUP_ARGS"); ok {
		infoCmd.Args = append(infoCmd.Args, strings.Fields(extraStartupArgs)...)
	}

	// Obtain the output directory path in the execution root.
	infoCmd.Args = append(infoCmd.Args,
		"info",
		query,
	)

	infoCmd.Environment.Set("DIST_DIR", config.DistDir())
	infoCmd.Environment.Set("SHELL", "/bin/bash")

	infoCmd.Dir = filepath.Join(config.OutDir(), "..")

	queryResult := strings.TrimSpace(string(infoCmd.OutputOrFatal()))
	return queryResult
}

// Main entry point to construct the Bazel build command line, environment
// variables and post-processing steps (e.g. converge output directories)
func runBazel(ctx Context, config Config) {
	ctx.BeginTrace(metrics.RunBazel, "bazel")
	defer ctx.EndTrace()

	// "droid" is the default ninja target.
	// TODO(b/160568333): stop hardcoding 'droid' to support building any
	// Ninja target.
	outputGroups := "droid"
	if len(config.ninjaArgs) > 0 {
		// At this stage, the residue slice of args passed to ninja
		// are the ninja targets to build, which can correspond directly
		// to ninja_build's output_groups.
		outputGroups = strings.Join(config.ninjaArgs, ",")
	}

	// Environment variables are the primary mechanism to pass information from
	// soong_ui configuration or context to Bazel.
	//
	// Use *_NINJA variables to pass the root-relative path of the combined,
	// kati-generated, soong-generated, and packaging Ninja files to Bazel.
	// Bazel reads these from the lunch() repository rule.
	config.environ.Set("COMBINED_NINJA", config.CombinedNinjaFile())
	config.environ.Set("KATI_NINJA", config.KatiBuildNinjaFile())
	config.environ.Set("PACKAGE_NINJA", config.KatiPackageNinjaFile())
	config.environ.Set("SOONG_NINJA", config.SoongNinjaFile())

	// `tools/bazel` is the default entry point for executing Bazel in the AOSP
	// source tree.
	bazelExecutable := filepath.Join("tools", "bazel")
	cmd := Command(ctx, config, "bazel", bazelExecutable)

	// Append custom startup flags to the Bazel command. Startup flags affect
	// the Bazel server itself, and any changes to these flags would incur a
	// restart of the server, losing much of the in-memory incrementality.
	if extraStartupArgs, ok := cmd.Environment.Get("BAZEL_STARTUP_ARGS"); ok {
		cmd.Args = append(cmd.Args, strings.Fields(extraStartupArgs)...)
	}

	// Start constructing the `build` command.
	actionName := "build"
	cmd.Args = append(cmd.Args,
		actionName,
		// Use output_groups to select the set of outputs to produce from a
		// ninja_build target.
		"--output_groups="+outputGroups,
		// Generate a performance profile
		"--profile="+filepath.Join(shared.BazelMetricsFilename(config.OutDir(), actionName)),
		"--slim_profile=true",
	)

	if config.UseRBE() {
		for _, envVar := range []string{
			// RBE client
			"RBE_compare",
			"RBE_exec_strategy",
			"RBE_invocation_id",
			"RBE_log_dir",
			"RBE_platform",
			"RBE_remote_accept_cache",
			"RBE_remote_update_cache",
			"RBE_server_address",
			// TODO: remove old FLAG_ variables.
			"FLAG_compare",
			"FLAG_exec_root",
			"FLAG_exec_strategy",
			"FLAG_invocation_id",
			"FLAG_log_dir",
			"FLAG_platform",
			"FLAG_remote_accept_cache",
			"FLAG_remote_update_cache",
			"FLAG_server_address",
		} {
			cmd.Args = append(cmd.Args,
				"--action_env="+envVar)
		}

		// We need to calculate --RBE_exec_root ourselves
		ctx.Println("Getting Bazel execution_root...")
		cmd.Args = append(cmd.Args, "--action_env=RBE_exec_root="+getBazelInfo(ctx, config, bazelExecutable, "execution_root"))
	}

	// Ensure that the PATH environment variable value used in the action
	// environment is the restricted set computed from soong_ui, and not a
	// user-provided one, for hermeticity reasons.
	if pathEnvValue, ok := config.environ.Get("PATH"); ok {
		cmd.Environment.Set("PATH", pathEnvValue)
		cmd.Args = append(cmd.Args, "--action_env=PATH="+pathEnvValue)
	}

	// Append custom build flags to the Bazel command. Changes to these flags
	// may invalidate Bazel's analysis cache.
	// These should be appended as the final args, so that they take precedence.
	if extraBuildArgs, ok := cmd.Environment.Get("BAZEL_BUILD_ARGS"); ok {
		cmd.Args = append(cmd.Args, strings.Fields(extraBuildArgs)...)
	}

	// Append the label of the default ninja_build target.
	cmd.Args = append(cmd.Args,
		"//:"+config.TargetProduct()+"-"+config.TargetBuildVariant(),
	)

	cmd.Environment.Set("DIST_DIR", config.DistDir())
	cmd.Environment.Set("SHELL", "/bin/bash")

	// Print the full command line for debugging purposes.
	ctx.Println(cmd.Cmd)

	// Execute the command at the root of the directory.
	cmd.Dir = filepath.Join(config.OutDir(), "..")
	ctx.Status.Status("Starting Bazel..")

	// Execute the build command.
	cmd.RunAndStreamOrFatal()

	// Post-processing steps start here. Once the Bazel build completes, the
	// output files are still stored in the execution root, not in $OUT_DIR.
	// Ensure that the $OUT_DIR contains the expected set of files by symlinking
	// the files from the execution root's output direction into $OUT_DIR.

	ctx.Println("Getting Bazel output_path...")
	outputBasePath := getBazelInfo(ctx, config, bazelExecutable, "output_path")
	// TODO: Don't hardcode out/ as the bazel output directory. This is
	// currently hardcoded as ninja_build.output_root.
	bazelNinjaBuildOutputRoot := filepath.Join(outputBasePath, "..", "out")

	symlinkOutdir(ctx, config, bazelNinjaBuildOutputRoot, ".")
}

// For all files F recursively under rootPath/relativePath, creates symlinks
// such that OutDir/F resolves to rootPath/F via symlinks.
func symlinkOutdir(ctx Context, config Config, rootPath string, relativePath string) {
	destDir := filepath.Join(rootPath, relativePath)
	os.MkdirAll(destDir, 0755)
	files, err := ioutil.ReadDir(destDir)
	if err != nil {
		ctx.Fatal(err)
	}
	for _, f := range files {
		destPath := filepath.Join(destDir, f.Name())
		srcPath := filepath.Join(config.OutDir(), relativePath, f.Name())
		if statResult, err := os.Stat(srcPath); err == nil {
			if statResult.Mode().IsDir() && f.IsDir() {
				// Directory under OutDir already exists, so recurse on its contents.
				symlinkOutdir(ctx, config, rootPath, filepath.Join(relativePath, f.Name()))
			} else if !statResult.Mode().IsDir() && !f.IsDir() {
				// File exists both in source and destination, and it's not a directory
				// in either location. Do nothing.
				// This can arise for files which are generated under OutDir outside of
				// soong_build, such as .bootstrap files.
			} else {
				// File is a directory in one location but not the other. Raise an error.
				ctx.Fatalf("Could not link %s to %s due to conflict", srcPath, destPath)
			}
		} else if os.IsNotExist(err) {
			// Create symlink srcPath -> fullDestPath.
			os.Symlink(destPath, srcPath)
		} else {
			ctx.Fatalf("Unable to stat %s: %s", srcPath, err)
		}
	}
}
