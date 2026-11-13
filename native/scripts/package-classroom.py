#!/usr/bin/env python3
"""Build a classroom ZIP from pinned official vendor archives, using the current checkout."""
import argparse
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import zipfile

NATIVE = Path(__file__).resolve().parents[1]
TARGETS = {
    "mac-arm64": ("darwin", "arm64", "darwin-arm64",
                  "e1a97e14c99c803e96c7339403282ea05a499c32f8d83defe9ef5ec66f979ed1",
                  "macos15-arm64.tar.gz", "6e89113f04f2af85d0a164573493db3a",
                  "darwin_arm64.tar.gz", "6c34c8ac087da20234ad90568e9485703af876de6e1be4aa26b6d9fba408b2f0"),
    "mac-intel": ("darwin", "amd64", "darwin-x64",
                  "dfd0dbd3e721503434df7b7205e719f61b3a3a31b2bcf9729b8b91fea240f080",
                  "macos15-x86_64.tar.gz", "bda70876ad005a83f78328c1c6a397ee",
                  "darwin_amd64.tar.gz", "2fa008b1460c5a3daccc0237d8b233ee9a16825865f7757ad1347faf4a2a3ade"),
    "windows-x64": ("windows", "amd64", "win-x64",
                     "0ae68406b42d7725661da979b1403ec9926da205c6770827f33aac9d8f26e821",
                     "winx64.zip", "2e833921898a9a030ea6bfe81bd811bc",
                     "windows_amd64.zip", "a76fde8519ad86168b5e017ad6df2401511fc1ee42f8f94727e36c545fdb061b"),
}


def digest(path, algorithm):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, algorithm).hexdigest()


def verify(path, expected, algorithm="sha256"):
    if digest(path, algorithm) != expected:
        raise SystemExit(f"Checksum mismatch for {path.name}")


def unpack(path, output):
    output.mkdir()
    if path.suffix == ".zip":
        with zipfile.ZipFile(path) as archive:
            archive.extractall(output)
    else:
        with tarfile.open(path) as archive:
            archive.extractall(output, filter="data")


def package(args, tmp):
    os_name, arch, node_platform, node_digest, mysql_suffix, mysql_digest, _, temporal_digest = TARGETS[args.target]
    verify(args.node, node_digest)
    verify(args.mysql, mysql_digest, "md5")
    verify(args.temporal, temporal_digest)
    if not args.mysql.name.endswith(mysql_suffix):
        raise SystemExit("MySQL archive platform does not match target")
    unpack(args.node, tmp / "node")
    unpack(args.mysql, tmp / "mysql")
    unpack(args.temporal, tmp / "temporal")
    stage = tmp / "stage"
    stage.mkdir()
    for name in ("activation.json", "activation-public.pem", "temporal.yaml",
                 "Start.cmd" if os_name == "windows" else "Start.command"):
        shutil.copy2(NATIVE / "classroom" / name, stage / name)

    node = next((tmp / "node").iterdir())
    node_destination = stage / "node"
    node_destination.mkdir()
    suffix = ".exe" if os_name == "windows" else ""
    node_relative = Path("node.exe") if suffix else Path("bin/node")
    (node_destination / node_relative).parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(node / node_relative, node_destination / node_relative)
    shutil.copy2(node / "LICENSE", node_destination / "LICENSE")
    mysql = next((tmp / "mysql").iterdir())
    shutil.copytree(mysql, stage / "mysql", ignore=shutil.ignore_patterns(
        "*.pdb", "*.lib", "*-debug*", "include", "man", "docs", "mysql-test"))
    (stage / "temporal").mkdir()
    shutil.copy2(args.temporal_license, stage / "temporal" / "LICENSE")
    for name in ("temporal-server", "temporal-sql-tool"):
        shutil.copy2(tmp / "temporal" / (name + suffix), stage / "temporal" / (name + suffix))
    shutil.copytree(NATIVE / "skills", stage / "skills")
    runtime = stage / "runtime"
    runtime.mkdir()
    for name in ("package.json", "package-lock.json"):
        shutil.copy2(NATIVE / name, runtime / name)
    shutil.copytree(NATIVE / "harness", runtime / "harness",
                    ignore=shutil.ignore_patterns("node_modules", "*.test.js"))
    if not (runtime / "harness" / "vault-editor.bundle.js").is_file():
        raise SystemExit("Run npm run build:vault-editor in native before packaging")
    # Install the target's optional native packages. Do not run target
    # postinstall scripts under the builder's operating system.
    cpu = "x64" if arch == "amd64" else arch
    npm_os = "win32" if os_name == "windows" else os_name
    subprocess.run(["npm", "ci", "--omit=dev", "--ignore-scripts", "--no-audit", "--no-fund",
                    "--os", npm_os, "--cpu", cpu], cwd=runtime, check=True)
    modules = runtime / "node_modules"
    plugin = modules / "@local/personal-workbench"
    if not plugin.is_symlink():
        raise SystemExit("Expected npm's local Harness plugin link")
    plugin.unlink()
    shutil.copytree(runtime / "harness", plugin)
    # Native node-pty prebuilds ship for all targets. Keep the target only.
    prebuilds = modules / "node-pty/prebuilds"
    for path in prebuilds.iterdir():
        if path.name != f"{npm_os}-{cpu}":
            shutil.rmtree(path)
    for symbol in prebuilds.rglob("*.pdb"):
        symbol.unlink()
    helper = prebuilds / f"{npm_os}-{cpu}/spawn-helper"
    if helper.exists():
        helper.chmod(0o755)
    for path in (modules / ".bin",):
        if path.is_dir():
            shutil.rmtree(path)
    if not (modules / f"@koromix/koffi-{npm_os}-{cpu}").is_dir():
        raise SystemExit("Target Koffi prebuild was not installed")
    for name, command in (("workbench-worker", "worker"), ("workbench-classroom", "classroom")):
        subprocess.run(["go", "build", "-trimpath", "-ldflags=-s -w",
                        "-o", str(stage / (name + suffix)), "./cmd/" + command],
                       env=dict(os.environ, GOOS=os_name, GOARCH=arch, CGO_ENABLED="0"),
                       cwd=NATIVE / "worker", check=True)

    start = "Start.cmd" if suffix else "Start.command"
    (stage / "先读我.txt").write_text(
        f"完整解压到自己的文件夹，双击 {start}。保持启动窗口运行。\n"
        "浏览器打开后，输入发码方给的激活码。进入工作台后配置自己的 DeepSeek API 密钥。\n"
        "课堂练习使用本机数据库，首次启动需要初始化。\n"
        "MySQL Community Server 与其他随包组件保留各自的许可。\n", encoding="utf-8")
    with zipfile.ZipFile(args.output, "w", zipfile.ZIP_DEFLATED, compresslevel=3) as archive:
        for path in sorted(stage.rglob("*")):
            if path.is_file():
                archive.write(path, Path("WorkbenchClassroom") / path.relative_to(stage))
    checksum = digest(args.output, "sha256")
    args.output.with_suffix(".zip.sha256").write_text(f"{checksum}  {args.output.name}\n")
    print(args.output, checksum, flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--target", required=True, choices=TARGETS)
    for arg in ("node", "mysql", "temporal", "temporal-license", "output"):
        parser.add_argument("--" + arg, required=True, type=lambda value: Path(value).resolve())
    args = parser.parse_args()
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="classroom-package-") as tmp:
        package(args, Path(tmp))


if __name__ == "__main__":
    main()
