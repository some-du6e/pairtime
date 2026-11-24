# Throwaway mutant probe for WP-HL1's PR A (scoring 0.7.0): each fix's
# cases pass on the clean tree, and with that fix reverted the named cases
# fail. Never merged. Run from the repository root: probe-hl1-a.py <group>,
# or probe-hl1-a.py --dry to apply every edit in memory and gofmt it.
import os
import subprocess
import sys

BRIDGED = ("a bridge prompt dated 20 min after the morning's resume, its batch sealed then, leaves doctor hearing why the night's"
           " minutes wait while the newest record the computer's own clock dated is under 5 min past the resume")
SCORE, AGENT, EVENT, LEDGER = "./pkg/score/", "./internal/agent/scorer/", "./pkg/event/", "./internal/agent/ledger/"

M = [
    # name, group, pkg, run, [(file, old, new)], [wanted failing tests]
    ("m312-cut-and-gap-clock", "score", SCORE, "^TestCutWaits$",
     [("pkg/score/presence.go", "\t\tif stampedRecord(e) {\n\t\t\tdated = max(dated, e.T)\n\t\t}\n\t\tswitch {\n", "\t\tif true {\n\t\t\tdated = max(dated, e.T)\n\t\t}\n\t\tswitch {\n")],
     ["TestCutWaits/a bridge prompt dated ahead of the device's clock and sealed with a stop hook doesn't end the suspend's reach before the resume queued behind that hook arrives, so the eight hours the machine slept stay asleep, as one Step reads them",
      "TestCutWaits/a bridge prompt dated ahead of the device's clock doesn't release the gap a resume ends: the gap holds the clock until a record the device dated is rules.late past its end, so a stop hook stamped a second before the resume and sealed one batch after it still ends the suspend's reach before anything in the gap goes final asleep"]),
    ("m61-final-from-run-start", "score", SCORE, "^TestReturnFinalFromTailEnd$",
     [("pkg/score/score.go", "func(r Return) int64 { return r.TailEnd })", "func(r Return) int64 { return r.RunStart })")],
     ["TestReturnFinalFromTailEnd"]),
    ("m61-no-later-prose", "score", SCORE, "^TestReturnRule$",
     [("pkg/score/bucket.go", "\tfor _, op := range cp.ops[cp.next:] {\n", "\tfor _, op := range cp.ops[cp.next:][:0] {\n")],
     ["TestReturnRule/an answer and a stop after the tail are prose that goes into the bucket later in the run: all 160 words count, and READ~r drains them from 140 s, since the bucket holds no prose before"]),
    ("m61-side-doors-counted", "score", SCORE, "^TestReturnRule$",
     [("pkg/score/bucket.go", "\t\t\tif e.prose && e.src == (source{}) {\n", "\t\t\tif e.prose {\n"),
      ("pkg/score/bucket.go", "\t\tif op.prose && op.src == (source{}) {\n", "\t\tif op.prose {\n")],
     ["TestReturnRule/prose a side door puts in a counted bucket isn't unread prose: an automated plugin turn's fill adds no word (DESIGN 5.2)"]),
    ("mcode-old-live-step", "score", SCORE, "^TestLiveUnknownCodes$",
     [("pkg/score/credit.go", "\tif code, ok := turnCode(&c.all, k); ok {\n\t\treturn code\n\t}\n\treturn event.ReasonNoAnchor\n",
       "\tswitch {\n\tcase covers(c.all.human, k), covers(c.all.noInput, k):\n\t\treturn event.ReasonAgentOnly\n\tcase covers(c.all.auto, k):\n\t\treturn event.ReasonAutomated\n\t}\n\treturn event.ReasonNoAnchor\n")],
     ["TestLiveUnknownCodes/while only a no_input turn runs, X.no_input, as in a sampled slot",
      "TestLiveUnknownCodes/an automated turn running beside a no_input one gives X.automated"]),
    ("mask-no-via", "score", SCORE, "^TestPromptClass$",
     [("pkg/score/turns.go", "\tkept := nativeVia(via) || (s.nativeSeq != 0 && s.nativeSeq < seq)\n", "\tkept := s.nativeSeq != 0 && s.nativeSeq < seq\n")],
     ["TestPromptClass/class low_effort: a plugin's question before a hook prompt that is its session's first hook record leaves a go-ahead low_effort, in a session with no hook session.start"]),
    ("mq-no-via", "score", SCORE, "^TestQuestions$",
     [("pkg/score/class.go", "\tif s.native || nativeVia(via) {\n", "\tif s.native {\n")],
     ["TestQuestions/a hook prompt reads no side door's question in a session with no hook or log record yet"]),
    ("mq-no-native-record", "score", SCORE, "^TestQuestions$",
     [("pkg/score/class.go", "\tif s.native || nativeVia(via) {\n", "\tif nativeVia(via) {\n")],
     ["TestQuestions/after a log record, a plugin prompt reads no side door's question"]),
    ("mtrack-no-error-paste", "score", SCORE, "^TestTrackClass$",
     [("pkg/score/class.go", "\tcase p.ErrorSig != \"\" && n < errorPasteMax:\n\t\treturn event.ClassErrorPaste\n", "")],
     ["TestTrackClass/an error line of 25 characters is an error paste",
      "TestTrackClass/still broken above an error line, 38 characters, is an error paste",
      "TestTrackClass/an error line under SUBSTANTIVE_CHARS is an error paste, not a go-ahead"]),
    ("mtrack-no-go-ahead", "score", SCORE, "^TestTrackClass$",
     [("pkg/score/class.go", "\tcase p.LowEffort:\n\t\treturn event.ClassLowEffort\n", "")],
     ["TestTrackClass/a go-ahead padded to 30 characters is a go-ahead"]),
    ("m323-any-uncredited", "score", SCORE, "^TestAgentOnlyAcrossProjects$",
     [("pkg/score/credit.go", "\t\t\t\tif agent {\n\t\t\t\t\tcr.agentUncredited++\n\t\t\t\t}\n", "\t\t\t\tcr.agentUncredited++\n")],
     ["TestAgentOnlyAcrossProjects"]),
    ("mlabel-no-line-feed", "score", EVENT, "^TestEventValidate$",
     [("pkg/event/check.go", "return r != '\\n' && unicode.IsControl(r)", "return unicode.IsControl(r)")],
     ["TestEventValidate/a track label keeps its line breaks"]),
    ("mask-no-native-record", "agent", AGENT, "^TestCarryQuestions$",
     [("pkg/score/turns.go", "\tkept := nativeVia(via) || (s.nativeSeq != 0 && s.nativeSeq < seq)\n", "\tkept := nativeVia(via)\n")],
     ["TestCarryQuestions/a plugin prompt in a session whose only hook record is its first session.start reads no plugin's question: the carry keeps that record, though its Start is the plugin's"]),
    ("mcarry-no-questions", "agent", AGENT, "^TestCarryQuestions$",
     [("internal/agent/scorer/window.go", "for _, e := range s.questions() {", "for _, e := range s.questions()[:0] {")],
     ["TestCarryQuestions/a hook prompt after the hooked turn's question, which a plugin's output follows, is an answer: the carry keeps the question, though its Output is the plugin's"]),
    ("mcarry-no-first-native", "agent", AGENT, "^TestCarryQuestions$",
     [("internal/agent/scorer/window.go", "\t\ts.FirstNative = bySeq(s.FirstNative, ev, false)\n", "\t\t_ = bySeq(s.FirstNative, ev, false)\n")],
     ["TestCarryQuestions/a plugin prompt in a session whose only hook record is its first session.start reads no plugin's question: the carry keeps that record, though its Start is the plugin's"]),
    ("mcarry-by-t", "agent", AGENT, "^TestCarryQuestions$",
     [("internal/agent/scorer/window.go", "(cur.Seq > ev.Seq) == last", "(cur.T > ev.T) == last")],
     ["TestCarryQuestions/an output a log reader read late, numbered after a question dated later, ends the question: the carry keeps the outputs by seq, not by t"]),
    ("mcarry-no-rebuilt-questions", "agent", AGENT, "^TestOlderPlaceNoSample$",
     [("internal/agent/scorer/loop.go", "\t\t\t\tsc.question(ev)\n", "\t\t\t\t_ = sc\n")],
     ["TestOlderPlaceNoSample/a place an agent before E-prompt-class-asked saved, at v 2, gets its sessions' question state folded again"]),
    ("m312-window-clock", "agent", AGENT, "^TestCarryReach$",
     [("internal/agent/scorer/window.go", "\t\tif clocked(e) {\n\t\t\tdated = max(dated, e.T)\n", "\t\tif true {\n\t\t\tdated = max(dated, e.T)\n")],
     ["TestCarryReach/a log reader's records dated ten minutes past the window's first sample don't make the stamped record before it end the carried sample's reach: the cut waits until a record the device's own clock dated is rules.late past it"]),
    ("m312-stall-clock", "agent", AGENT, "^(TestStallNote|TestStallWarning)$",
     [("internal/agent/scorer/stall.go", "case s.gapEnd > 0 && s.dated-s.gapEnd < late:", "case s.gapEnd > 0 && at-s.gapEnd < late:")],
     ["TestStallNote/with the newest record a bridge prompt dated 6 min past the gap's end but the newest one the computer dated only 4 min past it, the note still says the minutes wait for a record dated in that gap",
      "TestStallWarning/" + BRIDGED]),
    ("m312-stall-wiring", "agent", AGENT, "^(TestStallNote|TestStallWarning)$",
     [("internal/agent/scorer/stall.go", "\ts.dated = dated\n", "\ts.dated = high\n")],
     ["TestStallWarning/" + BRIDGED]),
    ("mwriter-plugin-via", "agent", LEDGER, "^TestContent$",
     [("internal/agent/ledger/writer.go", "w.q.Asked(ev.SessionKey, ev.Via)", "w.q.Asked(ev.SessionKey, event.ViaPlugin)")],
     ["TestContent/a plugin's output asking a question, before a hook prompt that is its session's first hook record (no hook session.start), leaves the prompt's f.pc low_effort"]),
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
