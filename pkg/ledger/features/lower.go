// SPDX-License-Identifier: AGPL-3.0-or-later

package features

import "sort"

// LowerUnicode is the Unicode version whose simple lowercase mapping
// version 1 of the features applies (spec 10.4 step 4).
const LowerUnicode = "15.0.0"

// lowerRun is a run of code points that lowercase by one offset: for i
// from 0 to count - 1, first + i*stride lowercases to itself plus delta.
type lowerRun struct {
	first, count, delta, stride int32
}

// lowerRuns is Unicode 15.0.0's simple lowercase mapping, field 13 of
// UnicodeData.txt, as the 182 runs that tools/ledger_ref.py and
// testdata/ledger/vectors-content.json list, in ascending order of first
// with no run reaching past the next one's first. It holds 1,433 mappings.
//
// The table is pinned here rather than taken from package unicode because
// Go's case tables follow newer Unicode versions, which add mappings: a
// toolchain bump would otherwise change what f.v = 1 means. The tests
// check this table against the vectors, and against unicode.ToLower apart
// from the mappings the vectors list as later.
var lowerRuns = [...]lowerRun{
	{0x0041, 26, 32, 1}, {0x00C0, 23, 32, 1}, {0x00D8, 7, 32, 1}, {0x0100, 24, 1, 2},
	{0x0130, 1, -199, 1}, {0x0132, 3, 1, 2}, {0x0139, 8, 1, 2}, {0x014A, 23, 1, 2},
	{0x0178, 1, -121, 1}, {0x0179, 3, 1, 2}, {0x0181, 1, 210, 1}, {0x0182, 2, 1, 2},
	{0x0186, 1, 206, 1}, {0x0187, 1, 1, 1}, {0x0189, 2, 205, 1}, {0x018B, 1, 1, 1},
	{0x018E, 1, 79, 1}, {0x018F, 1, 202, 1}, {0x0190, 1, 203, 1}, {0x0191, 1, 1, 1},
	{0x0193, 1, 205, 1}, {0x0194, 1, 207, 1}, {0x0196, 1, 211, 1}, {0x0197, 1, 209, 1},
	{0x0198, 1, 1, 1}, {0x019C, 1, 211, 1}, {0x019D, 1, 213, 1}, {0x019F, 1, 214, 1},
	{0x01A0, 3, 1, 2}, {0x01A6, 1, 218, 1}, {0x01A7, 1, 1, 1}, {0x01A9, 1, 218, 1}, {0x01AC, 1, 1, 1},
	{0x01AE, 1, 218, 1}, {0x01AF, 1, 1, 1}, {0x01B1, 2, 217, 1}, {0x01B3, 2, 1, 2},
	{0x01B7, 1, 219, 1}, {0x01B8, 1, 1, 1}, {0x01BC, 1, 1, 1}, {0x01C4, 1, 2, 1}, {0x01C5, 1, 1, 1},
	{0x01C7, 1, 2, 1}, {0x01C8, 1, 1, 1}, {0x01CA, 1, 2, 1}, {0x01CB, 9, 1, 2}, {0x01DE, 9, 1, 2},
	{0x01F1, 1, 2, 1}, {0x01F2, 2, 1, 2}, {0x01F6, 1, -97, 1}, {0x01F7, 1, -56, 1},
	{0x01F8, 20, 1, 2}, {0x0220, 1, -130, 1}, {0x0222, 9, 1, 2}, {0x023A, 1, 10795, 1},
	{0x023B, 1, 1, 1}, {0x023D, 1, -163, 1}, {0x023E, 1, 10792, 1}, {0x0241, 1, 1, 1},
	{0x0243, 1, -195, 1}, {0x0244, 1, 69, 1}, {0x0245, 1, 71, 1}, {0x0246, 5, 1, 2},
	{0x0370, 2, 1, 2}, {0x0376, 1, 1, 1}, {0x037F, 1, 116, 1}, {0x0386, 1, 38, 1}, {0x0388, 3, 37, 1},
	{0x038C, 1, 64, 1}, {0x038E, 2, 63, 1}, {0x0391, 17, 32, 1}, {0x03A3, 9, 32, 1},
	{0x03CF, 1, 8, 1}, {0x03D8, 12, 1, 2}, {0x03F4, 1, -60, 1}, {0x03F7, 1, 1, 1}, {0x03F9, 1, -7, 1},
	{0x03FA, 1, 1, 1}, {0x03FD, 3, -130, 1}, {0x0400, 16, 80, 1}, {0x0410, 32, 32, 1},
	{0x0460, 17, 1, 2}, {0x048A, 27, 1, 2}, {0x04C0, 1, 15, 1}, {0x04C1, 7, 1, 2}, {0x04D0, 48, 1, 2},
	{0x0531, 38, 48, 1}, {0x10A0, 38, 7264, 1}, {0x10C7, 1, 7264, 1}, {0x10CD, 1, 7264, 1},
	{0x13A0, 80, 38864, 1}, {0x13F0, 6, 8, 1}, {0x1C90, 43, -3008, 1}, {0x1CBD, 3, -3008, 1},
	{0x1E00, 75, 1, 2}, {0x1E9E, 1, -7615, 1}, {0x1EA0, 48, 1, 2}, {0x1F08, 8, -8, 1},
	{0x1F18, 6, -8, 1}, {0x1F28, 8, -8, 1}, {0x1F38, 8, -8, 1}, {0x1F48, 6, -8, 1},
	{0x1F59, 4, -8, 2}, {0x1F68, 8, -8, 1}, {0x1F88, 8, -8, 1}, {0x1F98, 8, -8, 1},
	{0x1FA8, 8, -8, 1}, {0x1FB8, 2, -8, 1}, {0x1FBA, 2, -74, 1}, {0x1FBC, 1, -9, 1},
	{0x1FC8, 4, -86, 1}, {0x1FCC, 1, -9, 1}, {0x1FD8, 2, -8, 1}, {0x1FDA, 2, -100, 1},
	{0x1FE8, 2, -8, 1}, {0x1FEA, 2, -112, 1}, {0x1FEC, 1, -7, 1}, {0x1FF8, 2, -128, 1},
	{0x1FFA, 2, -126, 1}, {0x1FFC, 1, -9, 1}, {0x2126, 1, -7517, 1}, {0x212A, 1, -8383, 1},
	{0x212B, 1, -8262, 1}, {0x2132, 1, 28, 1}, {0x2160, 16, 16, 1}, {0x2183, 1, 1, 1},
	{0x24B6, 26, 26, 1}, {0x2C00, 48, 48, 1}, {0x2C60, 1, 1, 1}, {0x2C62, 1, -10743, 1},
	{0x2C63, 1, -3814, 1}, {0x2C64, 1, -10727, 1}, {0x2C67, 3, 1, 2}, {0x2C6D, 1, -10780, 1},
	{0x2C6E, 1, -10749, 1}, {0x2C6F, 1, -10783, 1}, {0x2C70, 1, -10782, 1}, {0x2C72, 1, 1, 1},
	{0x2C75, 1, 1, 1}, {0x2C7E, 2, -10815, 1}, {0x2C80, 50, 1, 2}, {0x2CEB, 2, 1, 2},
	{0x2CF2, 1, 1, 1}, {0xA640, 23, 1, 2}, {0xA680, 14, 1, 2}, {0xA722, 7, 1, 2}, {0xA732, 31, 1, 2},
	{0xA779, 2, 1, 2}, {0xA77D, 1, -35332, 1}, {0xA77E, 5, 1, 2}, {0xA78B, 1, 1, 1},
	{0xA78D, 1, -42280, 1}, {0xA790, 2, 1, 2}, {0xA796, 10, 1, 2}, {0xA7AA, 1, -42308, 1},
	{0xA7AB, 1, -42319, 1}, {0xA7AC, 1, -42315, 1}, {0xA7AD, 1, -42305, 1}, {0xA7AE, 1, -42308, 1},
	{0xA7B0, 1, -42258, 1}, {0xA7B1, 1, -42282, 1}, {0xA7B2, 1, -42261, 1}, {0xA7B3, 1, 928, 1},
	{0xA7B4, 8, 1, 2}, {0xA7C4, 1, -48, 1}, {0xA7C5, 1, -42307, 1}, {0xA7C6, 1, -35384, 1},
	{0xA7C7, 2, 1, 2}, {0xA7D0, 1, 1, 1}, {0xA7D6, 2, 1, 2}, {0xA7F5, 1, 1, 1}, {0xFF21, 26, 32, 1},
	{0x10400, 40, 40, 1}, {0x104B0, 36, 40, 1}, {0x10570, 11, 39, 1}, {0x1057C, 15, 39, 1},
	{0x1058C, 7, 39, 1}, {0x10594, 2, 39, 1}, {0x10C80, 51, 64, 1}, {0x118A0, 32, 32, 1},
	{0x16E40, 32, 32, 1}, {0x1E900, 34, 34, 1},
}

// Lower returns r's simple lowercase mapping in Unicode 15.0.0, or r
// itself when it has none (spec 10.4 step 4). The mapping takes one code
// point to one: U+0130 becomes 'i', 'Σ' becomes 'σ' wherever it stands,
// and 'ß' stays.
func Lower(r rune) rune {
	if r < 0x80 {
		if 'A' <= r && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}
	// The last run that starts at or before r is the only one that can
	// hold it, because no run reaches past the next one's first.
	i := sort.Search(len(lowerRuns), func(i int) bool { return lowerRuns[i].first > r }) - 1
	if i < 0 {
		return r
	}
	run := lowerRuns[i]
	off := r - run.first
	if off%run.stride != 0 || off/run.stride >= run.count {
		return r
	}
	return r + run.delta
}
