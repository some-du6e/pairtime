// SPDX-License-Identifier: AGPL-3.0-or-later

package adapters

// LogLine says where in a harness's log file a raw record of SourceLog
// was read. The agent's tailers deliver each complete line of a log file
// as one raw record, in the order the file holds them (DESIGN 3.3;
// CP 3.2).
//
// A log reader can't keep a line's content in the memory it saves
// (Stateful), so the tailers deliver a file again, after a restart, from
// where that memory starts (LogReader.LogResume): the lines up to where
// they had read before come again as replays, from which the adapter
// rebuilds what it held in memory and makes no record.
type LogLine struct {
	// File names the file across the agent's runs: the tailers keep its
	// cursor under it, and the adapter its memory of the file. It is the
	// file's path on this machine, and like Path it stays on the device.
	File string
	// Path is the file's path on this machine.
	Path string
	// Offset is where the line starts in the file, and End where the next
	// one does. A line at Offset 0 is the file's first: the adapter starts
	// what it remembers of the file afresh there, as for a file it hasn't
	// read, or one the tailers found truncated or replaced.
	Offset, End int64
	// Replay says Parse is to rebuild its memory from the line and make no
	// record of it: the tailers handed it over before, after a restart or
	// a Rewind, or found it again in a file that was replaced.
	Replay bool
	// Idle says this is no line, but the tailers' word that the file has
	// had no new line for a while, at Offset, the end of what they read.
	// An adapter that waits for a later line before it writes a record can
	// write it now.
	Idle bool
	// TooLong says the line was longer than the tailers read, and Payload
	// holds none of it: the adapter counts it as a parse failure.
	TooLong bool
	// Entry is the line as the adapter's DecodeLog read it, when the
	// tailers decoded it on their own goroutine, and DecodeErr what
	// DecodeLog returned. With neither, Parse decodes Payload itself.
	Entry     any
	DecodeErr error
}

// LogRoot is a folder that holds a harness's log files.
type LogRoot struct {
	// Dir is the folder, an absolute path on this machine.
	Dir string
	// Depth is how many folders deep under Dir the log files lie, at
	// most: the tailers list no deeper.
	Depth int
	// Match reports whether rel, a path under Dir with '/' between its
	// parts, is one of the harness's log files.
	Match func(rel string) bool
	// Hot reports whether the folder rel under Dir, with '/' between its
	// parts, is one whose files the tailers watch while one of them is
	// being written. Nil watches every folder that holds a log file.
	Hot func(rel string) bool
}

// LogReader is an adapter that also reads its harness's log files, which
// the agent's tailers deliver to its Parse line by line, as raw records
// of SourceLog with Log set and Arrival.Via log (DESIGN 3.3, 3.4).
type LogReader interface {
	// LogRoots says where the harness keeps its logs on the machine env
	// describes. A root whose folder doesn't exist yet is watched for.
	LogRoots(env Env) []LogRoot
	// DecodeLog reads one line of a log file, whole, as Parse would. It
	// keeps nothing and is safe for concurrent use, so the tailers can
	// run it at their own priority, apart from the agent's worker, and
	// hand Parse the result in LogLine.Entry.
	DecodeLog(line []byte) (any, error)
	// LogPlace returns the folder a log record's line ran in, which Parse
	// resolves to a project (Options.Project), and the harness's id of
	// the session it belongs to, which orders it after the session's hook
	// calls: "" for either that the line doesn't name.
	LogPlace(raw RawRecord) (dir, session string)
	// LogResume returns where in file the adapter's memory of it starts:
	// the offset a restart must deliver the file again from, so that the
	// memory SaveState returned is rebuilt, and false for a file it
	// doesn't remember.
	LogResume(file string) (int64, bool)
	// ForgetLog drops what the adapter remembers of file, which is gone.
	ForgetLog(file string)
	// LogHint returns the log file a hook call names, such as Claude
	// Code's transcript_path, or "" for none. The agent has the tailers
	// read it soon, and watch its root, which may not be one LogRoots
	// knows, as when the harness's folder was moved for the shell the
	// person runs it from.
	LogHint(raw RawRecord) (path string, root LogRoot, ok bool)
}
