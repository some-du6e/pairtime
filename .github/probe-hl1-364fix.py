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
    ('masks-last-only', 'agent', './internal/agent/scorer/', '^(TestCarryQuestions|TestCarry|TestOlderPlaceQuestions)$', [('internal/agent/scorer/window.go', '\t\t\tfor _, seq := range readers {\n', '\t\t\tfor _, seq := range readers[:0] {\n')], ["TestCarryQuestions/a hook prompt after the hooked turn's question is an answer, though a log reader's output dated in the evening and numbered after the prompt folds into the carry: the carry keeps the question the prompt read", "TestCarry/of a session's outputs and stops the carry keeps the last ones and the last before each record it carries, and no others", 'TestCarry/a session keeps its start, last prompt, last output and last stop, and the output its last prompt read', 'TestOlderPlaceQuestions']),
    ('masks-no-carried-readers', 'agent', './internal/agent/scorer/', '^(TestCarryQuestions|TestCarry|TestOlderPlaceQuestions)$', [('internal/agent/scorer/window.go', '\tfor _, e := range s.events() {\n\t\treaders = append(readers, e.Seq)\n\t}\n', '')], ['TestCarry/a session keeps its start, last prompt, last output and last stop, and the output its last prompt read']),
    ('masks-no-window-readers', 'agent', './internal/agent/scorer/', '^(TestCarryQuestions|TestCarry|TestOlderPlaceQuestions)$', [('internal/agent/scorer/window.go', '\t\ts.keepAsks(readers[k])\n', '\t\ts.keepAsks(readers[k][:0])\n')], ["TestCarryQuestions/a hook prompt after the hooked turn's question is an answer, though a log reader's output dated in the evening and numbered after the prompt folds into the carry: the carry keeps the question the prompt read", 'TestOlderPlaceQuestions']),
    ('mrebuild-no-readers', 'agent', './internal/agent/scorer/', '^(TestOlderPlaceNoSample|TestOlderPlaceCarry|TestOlderPlaceQuestions)$', [('internal/agent/scorer/loop.go', '\t\ts.keepAsks(readers[k])\n', '\t\ts.keepAsks(readers[k][:0])\n')], ['TestOlderPlaceQuestions']),
    ('mstall-wording', 'agent', './internal/agent/scorer/', '^TestStallNote$', [('internal/agent/scorer/stall.go', "and then a record this computer's own clock dated (a sample, an os.power or a record the agent stamps itself) %s after that", 'and then any record %s after that')], ['TestStallNote/with samples that stopped while records came, the note says how long none has come']),
    ('meventof-no-track', 'agent', './internal/agent/scorer/', '^TestEventOf$', [('internal/agent/scorer/records.go', '\tcase *event.CLITrack:\n\t\tif p != nil {\n\t\t\tcp := *p\n\t\t\tev.Data, sig = &cp, &cp.ErrorSig\n\t\t}\n', '')], ["TestEventOf/a track call's error signature comes back from the keyed value in the body's f"]),
    ('mwriter-track-low-effort', 'ledger', './internal/agent/ledger/', '^TestContent$', [('internal/agent/ledger/writer.go', '\t\tcp.LowEffort = score.LowEffort(text)\n\t\tcp.ErrorSig = ""\n', '\t\tcp.ErrorSig = ""\n')], ["TestContent/a go-ahead padded to 30 characters is a track call's low_effort, from the text test the writer applies to the label"]),
    ('mwriter-text-guard', 'ledger', './internal/agent/ledger/', '^TestContent$', [('internal/agent/ledger/writer.go', '\t\tcp.LowEffort = score.LowEffort(text)\n\t\tcp.ErrorSig = ""\n', '\t\tcp.LowEffort = in.Text != nil && score.LowEffort(text)\n\t\tcp.ErrorSig = ""\n')], ['TestContent/a track label pkg/redact emptied is low_effort, since the writer tests the label it commits, even none']),
    ('mwriter-track-sig', 'ledger', './internal/agent/ledger/', '^TestContent$', [('internal/agent/ledger/writer.go', '\t\tif es, ok := w.feat.ErrorSig(text); ok {\n\t\t\tcp.ErrorSig = es.String()\n\t\t}\n\t\tev.Data = &cp\n', '\t\tev.Data = &cp\n')], ['TestContent/a track label that is an error line is an error paste, and f.es is its signature', 'TestContent/a track label with a line above its error line is an error paste, and f.es is the signature of the error line']),
    ('mreadsessions-focus-project', 'score', './pkg/score/', '^TestReadSessions$', [('pkg/score/returns.go', '\t\tif s.project == project {\n', '\t\tif s.project == f.project {\n')], ["TestReadSessions/with none hosted and a window that names no project, all the sessions of the act's project"]),
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
