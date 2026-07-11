"""Local Bazel helper for building packages with nfpm."""

load("@bazel_skylib//rules:common_settings.bzl", "BuildSettingInfo")

def _package_version(ctx):
    return ctx.attr._version[BuildSettingInfo].value

def _package_release(ctx):
    if ctx.attr.packager == "apk":
        return ctx.attr._apk_release[BuildSettingInfo].value

    return ctx.attr._release[BuildSettingInfo].value

def _package_output_name(ctx):
    package_name = ctx.attr.package_name
    version = _package_version(ctx)
    release = _package_release(ctx)
    packager = ctx.attr.packager
    arch = ctx.attr.arch

    if packager == "deb":
        return "%s_%s-%s_%s.deb" % (package_name, version, release, arch)
    if packager == "rpm":
        return "%s-%s-%s.%s.rpm" % (package_name, version, release, arch)
    if packager == "apk":
        return "%s-%s-r%s.%s.apk" % (package_name, version, release, arch)

    fail("unsupported packager %s" % packager)

def _file_substitution_key(prefix, file):
    if prefix:
        return "${NFPM_%s_%s}" % (prefix, file.basename.upper())

    return "${NFPM_%s}" % file.basename.upper()

def _staged_path(stage_path, prefix, file):
    if prefix:
        return "%s/%s/%s" % (stage_path, prefix.lower(), file.basename)

    return "%s/%s" % (stage_path, file.basename)

def _add_staged_file_substitution(substitutions, stage_path, prefix, file):
    key = _file_substitution_key(prefix, file)
    if key in substitutions:
        fail("duplicate nfpm substitution key %s from %s" % (key, file.short_path))

    substitutions[key] = _staged_path(stage_path, prefix, file)

def _nfpm_substitutions(ctx, stage_path):
    substitutions = {
        "${NFPM_ARCH}": ctx.attr.arch,
        "${NFPM_BINARY}": _staged_path(stage_path, "binary", ctx.file.binary),
        "${NFPM_RELEASE}": _package_release(ctx),
        "${NFPM_VERSION}": _package_version(ctx),
    }

    for script in ctx.files.scripts:
        _add_staged_file_substitution(substitutions, stage_path, "", script)

    for data_file in ctx.files.data_files:
        _add_staged_file_substitution(substitutions, stage_path, "DATA", data_file)

    return substitutions

def _add_stage_pair(args, stage_path, prefix, file):
    args.add(file)
    args.add(_staged_path(stage_path, prefix, file))

def _nfpm_package_impl(ctx):
    out = ctx.actions.declare_file(_package_output_name(ctx))
    stage_path = ctx.label.name + ".nfpm-stage"

    resolved_config = ctx.actions.declare_file(ctx.label.name + "_nfpm.yaml")
    ctx.actions.expand_template(
        template = ctx.file.config,
        output = resolved_config,
        substitutions = _nfpm_substitutions(ctx, stage_path),
    )

    args = ctx.actions.args()
    args.add(stage_path)
    args.add(resolved_config)
    args.add(ctx.executable._nfpm)
    args.add(ctx.attr.packager)
    args.add(out)
    _add_stage_pair(args, stage_path, "binary", ctx.file.binary)
    for script in ctx.files.scripts:
        _add_stage_pair(args, stage_path, "", script)
    for data_file in ctx.files.data_files:
        _add_stage_pair(args, stage_path, "DATA", data_file)

    ctx.actions.run(
        executable = ctx.executable._runner,
        arguments = [args],
        inputs = [resolved_config, ctx.file.binary] + ctx.files.scripts + ctx.files.data_files,
        outputs = [out],
        tools = [ctx.executable._nfpm],
        mnemonic = "NfpmPackage",
        progress_message = "Packaging %s (%s/%s)" % (ctx.label.name, ctx.attr.packager, ctx.attr.arch),
    )

    return [
        DefaultInfo(files = depset([out])),
        OutputGroupInfo(pkg = depset([out])),
    ]

nfpm_package = rule(
    implementation = _nfpm_package_impl,
    attrs = {
        "arch": attr.string(mandatory = True),
        "binary": attr.label(allow_single_file = True, mandatory = True),
        "config": attr.label(allow_single_file = True, mandatory = True),
        "data_files": attr.label_list(allow_files = True, default = []),
        "package_name": attr.string(mandatory = True),
        "packager": attr.string(mandatory = True, values = ["deb", "rpm", "apk"]),
        "scripts": attr.label_list(allow_files = True, default = []),
        "_apk_release": attr.label(default = Label("//tools/bazel:harness_apk_package_release")),
        "_nfpm": attr.label(
            default = Label("@multitool//tools/nfpm"),
            executable = True,
            cfg = "exec",
        ),
        "_release": attr.label(default = Label("//tools/bazel:harness_package_release")),
        "_runner": attr.label(
            default = Label("//tools/scripts:nfpm_package"),
            executable = True,
            cfg = "exec",
        ),
        "_version": attr.label(default = Label("//tools/bazel:harness_package_version")),
    },
)
