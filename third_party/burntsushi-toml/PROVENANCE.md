# BurntSushi/toml, with one patch

This directory is a copy of the Go module `github.com/BurntSushi/toml` with one patch. `go.mod`'s `replace` directive builds it in place of the module, for the client and the server alike.

- **Upstream:** <https://github.com/BurntSushi/toml>, tag `v1.6.0`, commit `52534926c55b4cd85b05aee90569dd0668b8cf30` (2025-12-18).
- **Fetched:** 2026-11-20, as the module zip from `proxy.golang.org`, whose hash matched `go.sum`'s `h1:dRaEfpa2VI55EwlIW72hMRHdWouJeRF7TPYhI+AUQjk=`.
- **License:** MIT, in `COPYING`, unchanged. These files keep their own license and their own headers, so ci.yml's license gate, which wants the AGPL header on every other `.go` file, skips `third_party/`.
- **Kept:** what the package builds from: `go.mod`, `COPYING`, `README.md`, the root package's non-test `.go` files and `internal/tz.go`. **Left out:** the tests and their data, `cmd/`, `internal/tag` and `internal/toml-test`, which only the test tools import, `_example`, `ossfuzz` and `.github`. Every kept file is byte for byte upstream's except the three the patch changes.
- **Why:** `internal/tz.go` read the computer's time zone offset in a package-level initializer. That loads `time.Local` in every process that links the module, before `main`, whatever the command. On Windows, when the zone's standard name isn't one of the English zone IDs Go knows, as on the CI runner's UTC and on Windows in other languages, loading `time.Local` reads every time zone under `HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Time Zones`. That took 29 to 34 ms of each `pairtime hook` call on `windows-latest` ([the erratum](../../docs/errata/2026-11-20-hook-p95-gate.md)). The patch reads the offset the first time the package needs it, under a `sync.Once`: when it decodes a datetime, date or time without an offset, or encodes a `time.Time`. It changes nothing else: the three locations keep their names and their offset, and each stays one `*time.Location` for the life of the process, which the encoder compares against.
- **Decided:** 2026-11-20, by the orchestrator for Karim, over writing a TOML reader of pairtime's own.
- **Upstream fix:** the same patch, together with the same change to `internal/tag` and `decode_test.go`, goes upstream as a pull request. Once a release has it, delete this directory and the `replace`, and require that release.

## The patch

```diff
--- a/internal/tz.go
+++ b/internal/tz.go
@@ -1,13 +1,15 @@
 package internal
 
-import "time"
+import (
+	"sync"
+	"time"
+)
 
 // Timezones used for local datetime, date, and time TOML types.
 //
 // The exact way times and dates without a timezone should be interpreted is not
 // well-defined in the TOML specification and left to the implementation. These
-// defaults to current local timezone offset of the computer, but this can be
-// changed by changing these variables before decoding.
+// defaults to current local timezone offset of the computer.
 //
 // TODO:
 // Ideally we'd like to offer people the ability to configure the used timezone
@@ -28,9 +30,37 @@
 //
 // Note that this behaviour is valid according to the TOML spec as the exact
 // behaviour is left up to implementations.
+//
+// The offset is read the first time one of them is used, not when the package
+// is initialized. Reading it loads time.Local, which on Windows can mean
+// reading every time zone in the registry, and a program that never handles a
+// datetime shouldn't pay for that when it starts.
 var (
-	localOffset   = func() int { _, o := time.Now().Zone(); return o }()
-	LocalDatetime = time.FixedZone("datetime-local", localOffset)
-	LocalDate     = time.FixedZone("date-local", localOffset)
-	LocalTime     = time.FixedZone("time-local", localOffset)
+	localOnce                           sync.Once
+	localDatetime, localDate, localTime *time.Location
 )
+
+func initLocal() {
+	_, o := time.Now().Zone()
+	localDatetime = time.FixedZone("datetime-local", o)
+	localDate = time.FixedZone("date-local", o)
+	localTime = time.FixedZone("time-local", o)
+}
+
+// LocalDatetime is the location of local datetimes.
+func LocalDatetime() *time.Location {
+	localOnce.Do(initLocal)
+	return localDatetime
+}
+
+// LocalDate is the location of local dates.
+func LocalDate() *time.Location {
+	localOnce.Do(initLocal)
+	return localDate
+}
+
+// LocalTime is the location of local times.
+func LocalTime() *time.Location {
+	localOnce.Do(initLocal)
+	return localTime
+}
--- a/parse.go
+++ b/parse.go
@@ -344,17 +344,19 @@
 
 var dtTypes = []struct {
 	fmt  string
-	zone *time.Location
+	zone func() *time.Location
 }{
-	{time.RFC3339Nano, time.Local},
+	{time.RFC3339Nano, localZone},
 	{"2006-01-02T15:04:05.999999999", internal.LocalDatetime},
 	{"2006-01-02", internal.LocalDate},
 	{"15:04:05.999999999", internal.LocalTime},
-	{"2006-01-02T15:04Z07:00", time.Local},
+	{"2006-01-02T15:04Z07:00", localZone},
 	{"2006-01-02T15:04", internal.LocalDatetime},
 	{"15:04", internal.LocalTime},
 }
 
+func localZone() *time.Location { return time.Local }
+
 func (p *parser) valueDatetime(it item) (any, tomlType) {
 	it.val = datetimeRepl.Replace(it.val)
 	var (
@@ -363,7 +365,7 @@
 		err error
 	)
 	for _, dt := range dtTypes {
-		t, err = time.ParseInLocation(dt.fmt, it.val, dt.zone)
+		t, err = time.ParseInLocation(dt.fmt, it.val, dt.zone())
 		if err == nil {
 			if missingLeadingZero(it.val, dt.fmt) {
 				p.panicErr(it, errParseDate{it.val})
--- a/encode.go
+++ b/encode.go
@@ -219,17 +219,17 @@
 	case time.Time: // Using TextMarshaler adds extra quotes, which we don't want.
 		format := time.RFC3339Nano
 		switch v.Location() {
-		case internal.LocalDatetime:
+		case internal.LocalDatetime():
 			format = "2006-01-02T15:04:05.999999999"
-		case internal.LocalDate:
+		case internal.LocalDate():
 			format = "2006-01-02"
-		case internal.LocalTime:
+		case internal.LocalTime():
 			format = "15:04:05.999999999"
 		}
 		switch v.Location() {
 		default:
 			enc.write(v.Format(format))
-		case internal.LocalDatetime, internal.LocalDate, internal.LocalTime:
+		case internal.LocalDatetime(), internal.LocalDate(), internal.LocalTime():
 			enc.write(v.In(time.UTC).Format(format))
 		}
 		return
```
