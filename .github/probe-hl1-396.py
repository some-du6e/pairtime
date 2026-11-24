# Throwaway mutant probe for WP-HL1's #364's review fixes: each fix's
# cases pass on the clean tree, and with that fix reverted the named cases
# fail. Never merged. Run from the repository root: probe-hl1-364fix.py <group>,
# or probe-hl1-364fix.py --dry to apply every edit in memory and gofmt it.
import os
import subprocess
import sys

BRIDGED = ("a bridge prompt dated 20 min after the morning's resume, its batch sealed then, leaves doctor hearing why the night's"
           " minutes wait while the newest record the computer's own clock dated is under 5 min past the resume")
SCORE, AGENT, EVENT, LEDGER = "./pkg/score/", "./internal/agent/scorer/", "./pkg/event/", "./internal/agent/ledger/"

M = [
    ('mreaders-carried-events', 'agent', './internal/agent/scorer/', '^(TestCarryPluginRun|TestCarry|TestCarryQuestions|TestOlderPlaceQuestions)$', [('internal/agent/scorer/window.go', '\tif s.Prompt != nil {\n\t\treaders = append(readers, s.Prompt.Seq)\n\t}\n', '\tfor _, e := range s.events() {\n\t\treaders = append(readers, e.Seq)\n\t}\n')], ["TestCarryPluginRun/a plugin-only session's turn, whose prompt and first outputs the carry holds while it runs, runs on to its last output, as one Step runs it", "TestCarry/of a session's outputs and stops the carry keeps the last ones and the last before the prompt it carries, and no others: no other carried record reads them"]),
    ('mreaders-window-any-record', 'agent', './internal/agent/scorer/', '^(TestCarryPluginRun|TestCarry|TestCarryQuestions|TestOlderPlaceQuestions)$', [('internal/agent/scorer/window.go', '\t\tif w.carry.Sessions[h.ev.SessionKey] != nil && readsAsks(&h.ev) {\n', '\t\tif w.carry.Sessions[h.ev.SessionKey] != nil {\n')], []),
]


def apply(edits, write):
    texts = {}
    for f, old, new in edits:
        s = texts.get(f) or open(f).read()
        n = s.count(old)
        if n != 1:
            raise SystemExit("%s: old text found %d times: %r" % (f, n, old[:80]))
        texts[f] = s.replace(old, new)
    if write:
        for f, s in texts.items():
            open(f, "w").write(s)
    return texts


def gotest(pkg, run):
    p = subprocess.run(["go", "test", "-count=1", "-v", "-run", run, pkg], capture_output=True, text=True)
    return p.returncode, p.stdout + p.stderr


def failed(out, name):
    return ("--- FAIL: %s (" % name.replace(" ", "_")) in out


def main():
    if sys.argv[1] == "--dry":
        d = sys.argv[2]
        for name, _, _, _, edits, _ in M:
            for f, s in apply(edits, False).items():
                path = os.path.join(d, name + "-" + os.path.basename(f))
                open(path, "w").write(s)
                p = subprocess.run(["gofmt", "-e", "-l", path], capture_output=True, text=True)
                print(name, f, "gofmt:", "ok" if p.returncode == 0 else p.stderr.strip())
        return
    group = sys.argv[1]
    ms = [m for m in M if m[1] == group]
    ok = True
    for pkg, run in dict.fromkeys((m[2], m[3]) for m in ms):
        code, out = gotest(pkg, run)
        print("clean %s %s: exit %d" % (pkg, run, code))
        if code != 0:
            print("::error::%s %s fails on the clean tree" % (pkg, run))
            print("\n".join(l for l in out.splitlines() if "--- FAIL" in l or "_test.go:" in l)[:6000])
            ok = False
    if not ok:
        sys.exit(1)
    rows = []
    for name, _, pkg, run, edits, want in ms:
        apply(edits, True)
        code, out = gotest(pkg, run)
        subprocess.run(["git", "checkout", "--"] + sorted({e[0] for e in edits}), check=True)
        if code == 0:
            rows.append((name, "SURVIVED", run))
            ok = False
            continue
        if "--- FAIL" not in out:
            rows.append((name, "BUILD BROKE", out[-1500:]))
            ok = False
            continue
        miss = [w for w in want if not failed(out, w)]
        if miss:
            rows.append((name, "MISSED", "; ".join(miss)))
            ok = False
        else:
            rows.append((name, "killed", "; ".join(want)))
        fails = [l.strip() for l in out.splitlines() if "--- FAIL" in l]
        print("== %s\n  " % name + "\n  ".join(fails[:12]))
    print()
    for r in rows:
        print("PROBE %s: %s: %s" % r)
        if r[1] != "killed":
            print("::error::mutant %s %s" % (r[0], r[1]))
    sys.exit(0 if ok else 1)


main()
