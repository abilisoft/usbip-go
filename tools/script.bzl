"""Rules for executable repository scripts."""

def _script_impl(ctx):
    out = ctx.actions.declare_file(ctx.label.name)
    ctx.actions.symlink(output = out, target_file = ctx.file.src, is_executable = True)

    return [DefaultInfo(
        executable = out,
        files = depset([out]),
        runfiles = ctx.runfiles(files = ctx.files.data + [out]),
    )]

_SCRIPT_ATTRS = {
    "data": attr.label_list(allow_files = True, default = []),
    "src": attr.label(
        allow_single_file = True,
        mandatory = True,
    ),
}

script_binary = rule(
    implementation = _script_impl,
    attrs = _SCRIPT_ATTRS,
    executable = True,
)

script_test = rule(
    implementation = _script_impl,
    attrs = _SCRIPT_ATTRS,
    test = True,
)
