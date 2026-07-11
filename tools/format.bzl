"""Formatter run-target rules."""

def _shell_word(value):
    return "'" + value.replace("'", "'\"'\"'") + "'"

def _words(values):
    return " ".join([_shell_word(value) for value in values])

def _format_tool_impl(ctx):
    out = ctx.actions.declare_file(ctx.label.name + "_runner")
    ctx.actions.expand_template(
        template = ctx.file._template,
        output = out,
        substitutions = {
            "__ARGS__": _words(ctx.attr.tool_args),
            "__EXTENSIONS__": _words(ctx.attr.extensions),
            "__NAME__": ctx.label.name,
            "__QUIET_STDOUT__": "1" if ctx.attr.quiet_stdout else "0",
            "__RUNFILES_NAME__": ctx.attr.runfiles_name,
            "__TOOL__": ctx.executable.tool.path,
        },
        is_executable = True,
    )

    runfiles = ctx.runfiles(files = [ctx.executable.tool])
    runfiles = runfiles.merge(ctx.attr.tool[DefaultInfo].default_runfiles)
    return [DefaultInfo(
        executable = out,
        files = depset([out]),
        runfiles = runfiles,
    )]

formatter = rule(
    implementation = _format_tool_impl,
    attrs = {
        "extensions": attr.string_list(),
        "quiet_stdout": attr.bool(),
        "runfiles_name": attr.string(),
        "tool_args": attr.string_list(),
        "tool": attr.label(
            allow_files = True,
            cfg = "target",
            executable = True,
            mandatory = True,
        ),
        "_template": attr.label(
            default = Label("//tools/scripts:format_tool.sh"),
            allow_single_file = True,
        ),
    },
    executable = True,
)

def _runner_file(target):
    files = target[DefaultInfo].files.to_list()
    if len(files) != 1:
        fail("formatter target must provide exactly one executable output")
    return files[0]

def _format_suite_impl(ctx):
    runner_files = [_runner_file(target) for target in ctx.attr.formatters]
    runner_paths = [runner.path for runner in runner_files]
    out = ctx.actions.declare_file(ctx.label.name + "_runner")
    ctx.actions.expand_template(
        template = ctx.file._template,
        output = out,
        substitutions = {"__RUNNERS__": _words(runner_paths)},
        is_executable = True,
    )

    runfiles = ctx.runfiles(files = runner_files)
    for formatter in ctx.attr.formatters:
        runfiles = runfiles.merge(formatter[DefaultInfo].default_runfiles)

    return [DefaultInfo(
        executable = out,
        files = depset([out]),
        runfiles = runfiles,
    )]

format_suite = rule(
    implementation = _format_suite_impl,
    attrs = {
        "formatters": attr.label_list(
            mandatory = True,
        ),
        "_template": attr.label(
            default = Label("//tools/scripts:format_suite.sh"),
            allow_single_file = True,
        ),
    },
    executable = True,
)
