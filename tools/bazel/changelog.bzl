"""Local Bazel helper for git-cliff changelog generation."""

def _changelog_impl(ctx):
    args_file = ctx.actions.declare_file(ctx.label.name + "_changelog_args")
    ctx.actions.write(
        output = args_file,
        content = "\n".join([
            "--cliff=" + ctx.executable._cliff.short_path,
            "--config=" + ctx.file.config.short_path,
        ]),
    )

    runner = ctx.actions.declare_file(ctx.label.name + "_runner")
    ctx.actions.symlink(output = runner, target_file = ctx.executable._runner, is_executable = True)

    runfiles = ctx.runfiles(files = [args_file, ctx.file.config]).merge(
        ctx.attr._runner[DefaultInfo].default_runfiles,
    ).merge(
        ctx.attr._cliff[DefaultInfo].default_runfiles,
    )

    return [
        DefaultInfo(executable = runner, runfiles = runfiles),
        RunEnvironmentInfo(environment = {"CHANGELOG_ARGS": args_file.short_path}),
    ]

changelog = rule(
    implementation = _changelog_impl,
    executable = True,
    attrs = {
        "config": attr.label(
            allow_single_file = [".toml"],
            mandatory = True,
        ),
        "_cliff": attr.label(
            default = Label("@multitool//tools/git-cliff"),
            executable = True,
            cfg = "exec",
        ),
        "_runner": attr.label(
            default = Label("//tools/scripts:changelog_runner"),
            executable = True,
            cfg = "exec",
        ),
    },
)
