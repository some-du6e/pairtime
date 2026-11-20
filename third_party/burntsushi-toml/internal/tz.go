package internal

import (
	"sync"
	"time"
)

// Timezones used for local datetime, date, and time TOML types.
//
// The exact way times and dates without a timezone should be interpreted is not
// well-defined in the TOML specification and left to the implementation. These
// defaults to current local timezone offset of the computer.
//
// TODO:
// Ideally we'd like to offer people the ability to configure the used timezone
// by setting Decoder.Timezone and Encoder.Timezone; however, this is a bit
// tricky: the reason we use three different variables for this is to support
// round-tripping – without these specific TZ names we wouldn't know which
// format to use.
//
// There isn't a good way to encode this right now though, and passing this sort
// of information also ties in to various related issues such as string format
// encoding, encoding of comments, etc.
//
// So, for the time being, just put this in internal until we can write a good
// comprehensive API for doing all of this.
//
// The reason they're exported is because they're referred from in e.g.
// internal/tag.
//
// Note that this behaviour is valid according to the TOML spec as the exact
// behaviour is left up to implementations.
//
// The offset is read the first time one of them is used, not when the package
// is initialized. Reading it loads time.Local, which on Windows can mean
// reading every time zone in the registry, and a program that never handles a
// datetime shouldn't pay for that when it starts.
var (
	localOnce                           sync.Once
	localDatetime, localDate, localTime *time.Location
)

func initLocal() {
	_, o := time.Now().Zone()
	localDatetime = time.FixedZone("datetime-local", o)
	localDate = time.FixedZone("date-local", o)
	localTime = time.FixedZone("time-local", o)
}

// LocalDatetime is the location of local datetimes.
func LocalDatetime() *time.Location {
	localOnce.Do(initLocal)
	return localDatetime
}

// LocalDate is the location of local dates.
func LocalDate() *time.Location {
	localOnce.Do(initLocal)
	return localDate
}

// LocalTime is the location of local times.
func LocalTime() *time.Location {
	localOnce.Do(initLocal)
	return localTime
}
