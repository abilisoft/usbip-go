"""GoReleaser runner rule backed by Bazel-provisioned tools."""

def _goreleaser_impl(ctx):
    args_file = ctx.actions.declare_file(ctx.label.name + "_goreleaser_args")
    lines = ["--goreleaser=" + ctx.executable.goreleaser.short_path]

    for entry in ctx.files.path_tools:
        lines.append("--path-prepend=" + _dirname(entry.short_path))

    lines.append("--")
    lines.extend(ctx.attr.command_args)
    ctx.actions.write(output = args_file, content = "\n".join(lines))

    runner = ctx.actions.declare_file(ctx.label.name + "_runner")
    ctx.actions.symlink(output = runner, target_file = ctx.executable._runner, is_executable = True)

    runfiles = ctx.runfiles(files = [args_file] + ctx.files.path_tools)
    for target in [ctx.attr.goreleaser, ctx.attr._runner] + ctx.attr.path_tools:
        runfiles = runfiles.merge(target[DefaultInfo].default_runfiles)

    return [
        DefaultInfo(executable = runner, runfiles = runfiles),
        RunEnvironmentInfo(environment = {"GORELEASER_RUNNER_ARGS": args_file.short_path}),
    ]

def _dirname(path):
    parts = path.rsplit("/", 1)
    if len(parts) == 1:
        return "."
    return parts[0]

goreleaser = rule(
    implementation = _goreleaser_impl,
    executable = True,
    attrs = {
        "command_args": attr.string_list(default = []),
        "goreleaser": attr.label(
            default = Label("@multitool//tools/goreleaser"),
            executable = True,
            cfg = "exec",
        ),
        "path_tools": attr.label_list(
            default = [
                Label("@go_sdk//:bin/go"),
                Label("@multitool//tools/cosign"),
                Label("@multitool//tools/syft"),
            ],
            allow_files = True,
        ),
        "_runner": attr.label(
            default = Label("//tools/scripts:goreleaser_runner"),
            executable = True,
            cfg = "exec",
        ),
    },
)
