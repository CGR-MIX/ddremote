"""Archive allowlisted source inputs with long-path support on Windows."""
from __future__ import annotations
import argparse
import fnmatch
from pathlib import Path, PurePosixPath
import tarfile

parser = argparse.ArgumentParser()
parser.add_argument("--root", required=True)
parser.add_argument("--output", required=True)
parser.add_argument("--dd-source", action="store_true")
parser.add_argument("inputs", nargs="+")
args = parser.parse_args()
root = Path(args.root).resolve()
output = Path(args.output).resolve()

def keep(info: tarfile.TarInfo) -> tarfile.TarInfo | None:
    name = info.name.replace("\\", "/")
    parts = PurePosixPath(name).parts
    if ".git" in parts:
        return None
    if args.dd_source:
        if any(p in {".gradle", ".cxx", "build", "__pycache__", "local.properties"} for p in parts):
            return None
        if name.startswith("apps/windows/payload/") and name.endswith(".zip"):
            return None
        if name.startswith("apps/android/app/src/main/jniLibs/") and name.endswith("/libddnetwork.so"):
            return None
        if any(fnmatch.fnmatch(parts[-1].lower(), pattern) for pattern in ("*.jks", "*.keystore", "*.pfx", "*.p12", "*.key", "*.apk", "*.exe", "*.log")):
            return None
    return info

with tarfile.open(output, "w:gz", compresslevel=1, format=tarfile.PAX_FORMAT) as archive:
    for item in args.inputs:
        source = (root / item).resolve()
        source.relative_to(root)  # Reject paths outside the declared source root.
        archive.add(source, arcname=item.replace("\\", "/"), filter=keep)

with tarfile.open(output, "r:gz") as archive:
    count = 0
    for info in archive:
        if keep(info) is None:
            raise RuntimeError(f"Excluded input found in archive: {info.name}")
        count += 1
print(f"Verified {count} archive members: {output.name}")
