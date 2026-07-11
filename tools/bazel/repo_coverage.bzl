"""Local Bazel helpers for checking source-filegroup coverage."""

def _repo_coverage_test_impl(ctx):
    lines = []

    for file in ctx.files.srcs:
        lines.append("--covered=" + file.short_path)

    for suffix in ctx.attr.include_suffixes:
        lines.append("--include-suffix=" + suffix)

    for prefix in ctx.attr.exclude_prefixes:
        lines.append("--exclude-prefix=" + prefix)

    args_file = ctx.actions.declare_file(ctx.label.name + "_repo_coverage_args")
    ctx.actions.write(output = args_file, content = "\n".join(lines))

    runner = ctx.actions.declare_file(ctx.label.name + "_runner")
    ctx.actions.symlink(output = runner, target_file = ctx.executable._runner, is_executable = True)

    runfiles = ctx.runfiles(files = ctx.files.srcs + [args_file]).merge(
        ctx.attr._runner[DefaultInfo].default_runfiles,
    )

    return [
        DefaultInfo(executable = runner, runfiles = runfiles),
        RunEnvironmentInfo(environment = {"REPO_COVERAGE_ARGS": args_file.short_path}),
    ]

repo_coverage_test = rule(
    implementation = _repo_coverage_test_impl,
    test = True,
    doc = "Fails when repo files matching configured suffixes are missing from the supplied source filegroups.",
    attrs = {
        "exclude_prefixes": attr.string_list(
            default = [
                "bazel-",
                "bazel-out/",
                "build/",
                "vendor/",
            ],
            doc = "Repo-relative prefixes to skip.",
        ),
        "include_suffixes": attr.string_list(
            mandatory = True,
            doc = "File suffixes that must be present in srcs, for example [\".go\"].",
        ),
        "srcs": attr.label_list(
            allow_files = True,
            mandatory = True,
            doc = "Files or filegroups expected to cover the matching repo files.",
        ),
        "_runner": attr.label(
            default = Label("//tools/scripts:repo_coverage_test_runner"),
            executable = True,
            cfg = "exec",
        ),
    },
)
