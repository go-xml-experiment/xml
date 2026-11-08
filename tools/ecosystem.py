#!/usr/bin/env python3
"""Plan 7.C: run public modules' own tests against encoding/xml and against the
v1 compatibility layer (github.com/Clickin/go-experiment-xml/v1) and classify the
difference.

  python3 tools/ecosystem.py WORKDIR REPORT.json NAME=URL[#"packages"] ...

For every module: shallow clone into WORKDIR/NAME (each in its own new directory),
record the commit, run `go test -json ./...` unmodified (baseline), then rewrite
every import of "encoding/xml" to the v1 package, point go.mod at this repository,
and run again. A test that passes in the baseline and does not pass in the second
run is a regression candidate; compile failures are reported per package.
"""
import json, os, re, subprocess, sys, time
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
V1 = "github.com/Clickin/go-experiment-xml/v1"
ROOT_MOD = "github.com/Clickin/go-experiment-xml"

IMPORT_BLOCK = re.compile(r'(?m)^(\s*)(?:(\w+|_|\.)\s+)?"encoding/xml"(\s*)$')
IMPORT_ONE = re.compile(r'(?m)^import\s+(?:(\w+|_|\.)\s+)?"encoding/xml"')


def sh(cmd, cwd, env=None, timeout=3600, check=False):
    e = dict(os.environ)
    if env:
        e.update(env)
    p = subprocess.run(cmd, cwd=cwd, env=e, capture_output=True, text=True, timeout=timeout)
    if check and p.returncode:
        raise RuntimeError(f"{cmd}: {p.stderr[-2000:]}")
    return p


def run_tests(d, pattern="./...", env=None, mod="-mod=mod"):
    t0 = time.time()
    p = sh(["go", "test", "-json", "-count=1", "-timeout", "20m", "-vet=off", mod] + pattern.split(), d, env=env, timeout=7200)
    res, pkgs, out = {}, {}, {}
    for line in p.stdout.splitlines():
        try:
            ev = json.loads(line)
        except ValueError:
            continue
        pk, test, act = ev.get("Package"), ev.get("Test"), ev.get("Action")
        if test:
            key = f"{pk}.{test}"
            if act in ("pass", "fail", "skip"):
                res[key] = act
            elif act == "output":
                out.setdefault(key, []).append(ev.get("Output", ""))
        elif act in ("pass", "fail", "skip"):
            pkgs[pk] = act
        elif act == "output":
            out.setdefault(pk, []).append(ev.get("Output", ""))
    # "FAIL pkg [build failed]" lines come on stderr/stdout of go test
    build_failed = sorted(set(re.findall(r"FAIL\s+(\S+)\s+\[(?:build|setup) failed\]", p.stdout + p.stderr)))
    return {"tests": res, "pkgs": pkgs, "output": {k: "".join(v)[-3000:] for k, v in out.items()},
            "build_failed": build_failed, "stderr": p.stderr[-4000:], "stdout_tail": "".join(l for l in p.stdout.splitlines(True) if not l.startswith("{"))[-3000:], "seconds": round(time.time() - t0, 1)}


def rewrite_imports(d, include_vendor=False):
    n = 0
    for f in Path(d).rglob("*.go"):
        if ".git" in f.parts or (not include_vendor and "vendor" in f.parts):
            continue
        if "Clickin" in f.parts:
            continue  # the replacement itself
        s = f.read_text(errors="replace")
        if '"encoding/xml"' not in s:
            continue

        def blk(m):
            alias = m.group(2) or "xml"
            return f'{m.group(1)}{alias} "{V1}"{m.group(3)}'

        def one(m):
            alias = m.group(1) or "xml"
            return f'import {alias} "{V1}"'
        s2 = IMPORT_BLOCK.sub(blk, s)
        s2 = IMPORT_ONE.sub(one, s2)
        if s2 != s:
            f.write_text(s2)
            n += 1
    return n


def main():
    work, report = Path(sys.argv[1]), Path(sys.argv[2])
    mods = [a.split("=", 1) for a in sys.argv[3:]]  # NAME=URL or NAME=URL#"pkg pattern ..."
    out = json.load(open(report)) if report.exists() else {}
    for name, url in mods:
        url, _, pattern = url.partition("#")
        pattern = pattern or "./..."
        d = work / name
        r = {"url": url}
        if not d.exists():
            sh(["git", "clone", "--depth", "1", url, str(d)], work, check=True)
        r["commit"] = sh(["git", "rev-parse", "HEAD"], d).stdout.strip()
        r["files_importing_encoding_xml"] = int(sh(["sh", "-c", "grep -rl '\"encoding/xml\"' --include=*.go . | grep -v vendor | wc -l"], d).stdout.strip() or 0)
        gomod = d / "go.mod"
        if not gomod.exists():
            r["error"] = "no go.mod"
            out[name] = r
            continue
        sh(["go", "mod", "download"], d)
        r["baseline"] = run_tests(d, pattern)
        # variant
        sh(["git", "stash", "-u"], d)  # nothing to stash normally
        r["rewritten_files"] = rewrite_imports(d)
        sh(["go", "mod", "edit", f"-require={ROOT_MOD}@v0.0.0", f"-replace={ROOT_MOD}={REPO}"], d, check=True)
        # Replace encoding/xml in the whole dependency graph, as a toolchain-level
        # change would: vendor every dependency and rewrite the vendored sources too.
        # A package that passes an xml.Decoder to a dependency would not compile otherwise.
        vend = d / "vendor"
        if vend.exists():
            sh(["rm", "-rf", str(vend)], d)
        # the module may not use the v1 package itself, so make it a dependency for vendoring
        stub = d / "zzecosystem"
        stub.mkdir(exist_ok=True)
        (stub / "stub.go").write_text('package zzecosystem\n\nimport _ "' + V1 + '"\n')
        sh(["go", "mod", "tidy", "-e"], d)
        pv = sh(["go", "mod", "vendor"], d)
        r["vendor_error"] = pv.stderr[-1500:] if pv.returncode else ""
        r["rewritten_vendor_files"] = rewrite_imports(vend, include_vendor=True) if pv.returncode == 0 else 0
        r["v1"] = run_tests(d, pattern, mod="-mod=vendor" if pv.returncode == 0 else "-mod=mod")
        base, v1 = r["baseline"]["tests"], r["v1"]["tests"]
        r["regressions"] = sorted(k for k, v in base.items() if v == "pass" and v1.get(k) != "pass")
        r["new_failures_pkgs"] = sorted(k for k, v in r["baseline"]["pkgs"].items() if v == "pass" and r["v1"]["pkgs"].get(k) != "pass")
        r["baseline_failing"] = sorted(k for k, v in base.items() if v == "fail")
        r["counts"] = {"baseline_pass": sum(v == "pass" for v in base.values()), "baseline_fail": sum(v == "fail" for v in base.values()),
                       "v1_pass": sum(v == "pass" for v in v1.values()), "v1_fail": sum(v == "fail" for v in v1.values())}
        out[name] = r
        report.write_text(json.dumps(out, indent=1))
        sh(["rm", "-rf", str(d / "vendor"), str(stub)], d)
        sh(["git", "checkout", "--", "."], d)
        print(name, r["counts"], "regressions:", len(r["regressions"]), "build_failed:", r["v1"]["build_failed"], flush=True)


if __name__ == "__main__":
    main()
