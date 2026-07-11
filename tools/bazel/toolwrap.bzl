"""Local Bazel helpers for wrapping repository tools as tests."""

def _toolwrap_test_impl(ctx):
    args_file = ctx.actions.declare_file(ctx.label.name + "_toolwrap_args")
    ctx.actions.write(output = args_file, content = "\n".join(_toolwrap_args(ctx)))

    runner = ctx.actions.declare_file(ctx.label.name + "_runner")
    ctx.actions.symlink(output = runner, target_file = ctx.executable._runner, is_executable = True)

    return [
        DefaultInfo(executable = runner, runfiles = _toolwrap_runfiles(ctx, args_file)),
        RunEnvironmentInfo(environment = {"TOOLWRAP_ARGS": args_file.short_path}),
    ]

def _toolwrap_args(ctx):
    lines = ["--tool=" + ctx.executable.tool.short_path]

    for entry in ctx.files.path_tools:
        lines.append("--path-prepend=" + _dirname(entry.short_path))

    if ctx.attr.dereference:
        lines.append("--dereference=true")

    lines.append("--")

    for arg in ctx.attr.tool_args:
        if "$(locations" in arg:
            fail("tool_args cannot use $(locations ...); pass file labels with arg_files instead")

        lines.append(ctx.expand_location(arg, ctx.attr.data))

    for file in ctx.files.arg_files:
        lines.append(file.short_path)

    return lines

def _toolwrap_runfiles(ctx, args_file):
    runfiles = ctx.runfiles(
        files = ctx.files.arg_files + ctx.files.data + ctx.files.path_tools + [args_file],
    )

    for target in ctx.attr.data + ctx.attr.path_tools + [ctx.attr.tool, ctx.attr._runner]:
        runfiles = runfiles.merge(target[DefaultInfo].default_runfiles)

    return runfiles

def _dirname(path):
    parts = path.rsplit("/", 1)
    if len(parts) == 1:
        return "."
    return parts[0]

toolwrap_test = rule(
    implementation = _toolwrap_test_impl,
    test = True,
    attrs = {
        "arg_files": attr.label_list(allow_files = True, default = []),
        "data": attr.label_list(allow_files = True, default = []),
        "dereference": attr.bool(default = False),
        "path_tools": attr.label_list(allow_files = True, default = []),
        "tool": attr.label(executable = True, cfg = "exec", mandatory = True),
        "tool_args": attr.string_list(default = []),
        "_runner": attr.label(
            default = Label("//tools/scripts:toolwrap_test_runner"),
            executable = True,
            cfg = "exec",
        ),
    },
)

toolwrap_binary = rule(
    implementation = _toolwrap_test_impl,
    executable = True,
    attrs = {
        "arg_files": attr.label_list(allow_files = True, default = []),
        "data": attr.label_list(allow_files = True, default = []),
        "dereference": attr.bool(default = False),
        "path_tools": attr.label_list(allow_files = True, default = []),
        "tool": attr.label(executable = True, cfg = "exec", mandatory = True),
        "tool_args": attr.string_list(default = []),
        "_runner": attr.label(
            default = Label("//tools/scripts:toolwrap_test_runner"),
            executable = True,
            cfg = "exec",
        ),
    },
)
