// SPDX-License-Identifier: AGPL-3.0-or-later

package redact

// The IBAN formats of Presidio's iban_patterns.py (presidio-analyzer
// 2.2.366, MIT, Copyright (c) Presidio Contributors), ported line for line:
// the same building blocks, the same comments, one entry per country.
// testdata/redact/presidio/README.md has the provenance.
const (
	ibanCK = "[0-9]{2}[ ]?" // check digits

	ibanA  = "[A-Z][ ]?"
	ibanA2 = "([A-Z][ ]?){2}"
	ibanA3 = "([A-Z][ ]?){3}"
	ibanA4 = "([A-Z][ ]?){4}"

	ibanC  = "[a-zA-Z0-9][ ]?"
	ibanC2 = "([a-zA-Z0-9][ ]?){2}"
	ibanC3 = "([a-zA-Z0-9][ ]?){3}"
	ibanC4 = "([a-zA-Z0-9][ ]?){4}"

	ibanN  = "[0-9][ ]?"
	ibanN2 = "([0-9][ ]?){2}"
	ibanN3 = "([0-9][ ]?){3}"
	ibanN4 = "([0-9][ ]?){4}"
)

// ibanFormats is Presidio's regex_per_country.
var ibanFormats = map[string]string{
	// Albania (8n, 16c) ALkk bbbs sssx cccc cccc cccc cccc
	"AL": "(AL)" + ibanCK + ibanN4 + ibanN4 + ibanC4 + ibanC4 + ibanC4 + ibanC4,
	// Andorra (8n, 12c) ADkk bbbb ssss cccc cccc cccc
	"AD": "(AD)" + ibanCK + ibanN4 + ibanN4 + ibanC4 + ibanC4 + ibanC4,
	// Austria (16n) ATkk bbbb bccc cccc cccc
	"AT": "(AT)" + ibanCK + ibanN4 + ibanN4 + ibanN4 + ibanN4,
	// Azerbaijan    (4c,20n) AZkk bbbb cccc cccc cccc cccc cccc
	"AZ": "(AZ)" + ibanCK + ibanC4 + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN4,
	// Bahrain   (4a,14c)    BHkk bbbb cccc cccc cccc cc
	"BH": "(BH)" + ibanCK + ibanA4 + ibanC4 + ibanC4 + ibanC4 + ibanC2,
	// Belarus (4c, 4n, 16c)   BYkk bbbb aaaa cccc cccc cccc cccc
	"BY": "(BY)" + ibanCK + ibanC4 + ibanN4 + ibanC4 + ibanC4 + ibanC4 + ibanC4,
	// Belgium (12n)   BEkk bbbc cccc ccxx
	"BE": "(BE)" + ibanCK + ibanN4 + ibanN4 + ibanN4,
	// Bosnia and Herzegovina    (16n)   BAkk bbbs sscc cccc ccxx
	"BA": "(BA)" + ibanCK + ibanN4 + ibanN4 + ibanN4 + ibanN4,
	// Brazil (23n,1a,1c) BRkk bbbb bbbb ssss sccc cccc ccct n
	"BR": "(BR)" + ibanCK + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN3 + ibanA + ibanC,
	// Bulgaria  (4a,6n,8c)  BGkk bbbb ssss ttcc cccc cc
	"BG": "(BG)" + ibanCK + ibanA4 + ibanN4 + ibanN + ibanN + ibanC2 + ibanC4 + ibanC2,
	// Costa Rica    (18n)   CRkk 0bbb cccc cccc cccc cc (0 = always zero)
	"CR": "(CR)" + ibanCK + "[0]" + ibanN3 + ibanN4 + ibanN4 + ibanN4 + ibanN2,
	// Croatia   (17n)   HRkk bbbb bbbc cccc cccc c
	"HR": "(HR)" + ibanCK + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN,
	// Cyprus    (8n,16c)    CYkk bbbs ssss cccc cccc cccc cccc
	"CY": "(CY)" + ibanCK + ibanN4 + ibanN4 + ibanC4 + ibanC4 + ibanC4 + ibanC4,
	// Czech Republic    (20n)   CZkk bbbb ssss sscc cccc cccc
	"CZ": "(CZ)" + ibanCK + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN4,
	// Denmark   (14n)   DKkk bbbb cccc cccc cc
	"DK": "(DK)" + ibanCK + ibanN4 + ibanN4 + ibanN4 + ibanN2,
	// Dominican Republic    (4a,20n)    DOkk bbbb cccc cccc cccc cccc cccc
	"DO": "(DO)" + ibanCK + ibanA4 + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN4,
	// EAt Timor    (19n) TLkk bbbc cccc cccc cccc cxx
	"TL": "(TL)" + ibanCK + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN3,
	// Estonia   (16n) EEkk bbss cccc cccc cccx
	"EE": "(EE)" + ibanCK + ibanN4 + ibanN4 + ibanN4 + ibanN4,
	// Faroe Islands    (14n) FOkk bbbb cccc cccc cx
	"FO": "(FO)" + ibanCK + ibanN4 + ibanN4 + ibanN4 + ibanN2,
	// Finland   (14n) FIkk bbbb bbcc cccc cx
	"FI": "(FI)" + ibanCK + ibanN4 + ibanN4 + ibanN4 + ibanN2,
	// France    (10n,11c,2n) FRkk bbbb bsss sscc cccc cccc cxx
	"FR": "(FR)" + ibanCK + ibanN4 + ibanN4 + ibanN2 + ibanC2 + ibanC4 + ibanC4 + ibanC + ibanN2,
	// Georgia   (2c,16n)  GEkk bbcc cccc cccc cccc cc
	"GE": "(GE)" + ibanCK + ibanC2 + ibanN2 + ibanN4 + ibanN4 + ibanN4 + ibanN2,
	// Germany   (18n) DEkk bbbb bbbb cccc cccc cc
	"DE": "(DE)" + ibanCK + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN2,
	// Gibraltar (4a,15c)  GIkk bbbb cccc cccc cccc ccc
	"GI": "(GI)" + ibanCK + ibanA4 + ibanC4 + ibanC4 + ibanC4 + ibanC3,
	// Greece    (7n,16c)  GRkk bbbs sssc cccc cccc cccc ccc
	"GR": "(GR)" + ibanCK + ibanN4 + ibanN3 + ibanC + ibanC4 + ibanC4 + ibanC4 + ibanC3,
	// Greenland     (14n) GLkk bbbb cccc cccc cc
	"GL": "(GL)" + ibanCK + ibanN4 + ibanN4 + ibanN4 + ibanN2,
	// Guatemala (4c,20c)  GTkk bbbb mmtt cccc cccc cccc cccc
	"GT": "(GT)" + ibanCK + ibanC4 + ibanC4 + ibanC4 + ibanC4 + ibanC4 + ibanC4,
	// Hungary   (24n) HUkk bbbs sssx cccc cccc cccc cccx
	"HU": "(HU)" + ibanCK + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN4,
	// Iceland   (22n) ISkk bbbb sscc cccc iiii iiii ii
	"IS": "(IS)" + ibanCK + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN2,
	// Ireland   (4c,14n)  IEkk aaaa bbbb bbcc cccc cc
	"IE": "(IE)" + ibanCK + ibanC4 + ibanN4 + ibanN4 + ibanN4 + ibanN2,
	// Israel (19n) ILkk bbbn nncc cccc cccc ccc
	"IL": "(IL)" + ibanCK + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN3,
	// Italy (1a,10n,12c)  ITkk xbbb bbss sssc cccc cccc ccc
	"IT": "(IT)" + ibanCK + ibanA + ibanN3 + ibanN4 + ibanN3 + ibanC + ibanC3 + ibanC + ibanC4 + ibanC3,
	// Jordan    (4a,22n)  JOkk bbbb ssss cccc cccc cccc cccc cc
	"JO": "(JO)" + ibanCK + ibanA4 + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN2,
	// Kazakhstan    (3n,13c)  KZkk bbbc cccc cccc cccc
	"KZ": "(KZ)" + ibanCK + ibanN3 + ibanC + ibanC4 + ibanC4 + ibanC4,
	// Kosovo    (4n,10n,2n)   XKkk bbbb cccc cccc cccc
	"XK": "(XK)" + ibanCK + ibanN4 + ibanN4 + ibanN4 + ibanN4,
	// Kuwait    (4a,22c)  KWkk bbbb cccc cccc cccc cccc cccc cc
	"KW": "(KW)" + ibanCK + ibanA4 + ibanC4 + ibanC4 + ibanC4 + ibanC4 + ibanC4 + ibanC2,
	// Latvia    (4a,13c)  LVkk bbbb cccc cccc cccc c
	"LV": "(LV)" + ibanCK + ibanA4 + ibanC4 + ibanC4 + ibanC4 + ibanC,
	// Lebanon   (4n,20c)  LBkk bbbb cccc cccc cccc cccc cccc
	"LB": "(LB)" + ibanCK + ibanN4 + ibanC4 + ibanC4 + ibanC4 + ibanC4 + ibanC4,
	// LiechteNtein (5n,12c)  LIkk bbbb bccc cccc cccc c
	"LI": "(LI)" + ibanCK + ibanN4 + ibanN + ibanC3 + ibanC4 + ibanC4 + ibanC,
	// Lithuania (16n) LTkk bbbb bccc cccc cccc
	"LT": "(LT)" + ibanCK + ibanN4 + ibanN4 + ibanN4 + ibanN4,
	// Luxembourg    (3n,13c)  LUkk bbbc cccc cccc cccc
	"LU": "(LU)" + ibanCK + ibanN3 + ibanC + ibanC4 + ibanC4 + ibanC4,
	// Malta (4a,5n,18c)   MTkk bbbb ssss sccc cccc cccc cccc ccc
	"MT": "(MT)" + ibanCK + ibanA4 + ibanN4 + ibanN + ibanC3 + ibanC4 + ibanC4 + ibanC4 + ibanC3,
	// Mauritania    (23n) MRkk bbbb bsss sscc cccc cccc cxx
	"MR": "(MR)" + ibanCK + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN3,
	// Mauritius (4a,19n,3a)   MUkk bbbb bbss cccc cccc cccc 000m mm
	"MU": "(MU)" + ibanCK + ibanA4 + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN3 + ibanA,
	// Moldova   (2c,18c)  MDkk bbcc cccc cccc cccc cccc
	"MD": "(MD)" + ibanCK + ibanC4 + ibanC4 + ibanC4 + ibanC4 + ibanC4,
	// Monaco    (10n,11c,2n)  MCkk bbbb bsss sscc cccc cccc cxx
	"MC": "(MC)" + ibanCK + ibanN4 + ibanN4 + ibanN2 + ibanC2 + ibanC4 + ibanC4 + ibanC + ibanN2,
	// Montenegro    (18n) MEkk bbbc cccc cccc cccc xx
	"ME": "(ME)" + ibanCK + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN2,
	// Netherlands   (4a,10n)  NLkk bbbb cccc cccc cc
	"NL": "(NL)" + ibanCK + ibanA4 + ibanN4 + ibanN4 + ibanN2,
	// North Macedonia   (3n,10c,2n)   MKkk bbbc cccc cccc cxx
	"MK": "(MK)" + ibanCK + ibanN3 + ibanC + ibanC4 + ibanC4 + ibanC + ibanN2,
	// Norway    (11n) NOkk bbbb cccc ccx
	"NO": "(NO)" + ibanCK + ibanN4 + ibanN4 + ibanN3,
	// Pakistan  (4c,16n)  PKkk bbbb cccc cccc cccc cccc
	"PK": "(PK)" + ibanCK + ibanC4 + ibanN4 + ibanN4 + ibanN4 + ibanN4,
	// Palestinian territories   (4c,21n)  PSkk bbbb xxxx xxxx xccc cccc cccc c
	"PS": "(PS)" + ibanCK + ibanC4 + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN,
	// Poland    (24n) PLkk bbbs sssx cccc cccc cccc cccc
	"PL": "(PL)" + ibanCK + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN4,
	// Portugal  (21n) PTkk bbbb ssss cccc cccc cccx x
	"PT": "(PT)" + ibanCK + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN,
	// Qatar (4a,21c)  QAkk bbbb cccc cccc cccc cccc cccc c
	"QA": "(QA)" + ibanCK + ibanA4 + ibanC4 + ibanC4 + ibanC4 + ibanC4 + ibanC,
	// Romania   (4a,16c)  ROkk bbbb cccc cccc cccc cccc
	"RO": "(RO)" + ibanCK + ibanA4 + ibanC4 + ibanC4 + ibanC4 + ibanC4,
	// San Marino    (1a,10n,12c)  SMkk xbbb bbss sssc cccc cccc ccc
	"SM": "(SM)" + ibanCK + ibanA + ibanN3 + ibanN4 + ibanN3 + ibanC + ibanC4 + ibanC4 + ibanC3,
	// Saudi Arabia  (2n,18c)  SAkk bbcc cccc cccc cccc cccc
	"SA": "(SA)" + ibanCK + ibanN2 + ibanC2 + ibanC4 + ibanC4 + ibanC4 + ibanC4,
	// Serbia    (18n) RSkk bbbc cccc cccc cccc xx
	"RS": "(RS)" + ibanCK + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN2,
	// Slovakia  (20n) SKkk bbbb ssss sscc cccc cccc
	"SK": "(SK)" + ibanCK + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN4,
	// Slovenia  (15n) SIkk bbss sccc cccc cxx
	"SI": "(SI)" + ibanCK + ibanN4 + ibanN4 + ibanN4 + ibanN3,
	// Spain (20n) ESkk bbbb ssss xxcc cccc cccc
	"ES": "(ES)" + ibanCK + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN4,
	// Sweden    (20n) SEkk bbbc cccc cccc cccc cccc
	"SE": "(SE)" + ibanCK + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN4,
	// Switzerland   (5n,12c)  CHkk bbbb bccc cccc cccc c
	"CH": "(CH)" + ibanCK + ibanN4 + ibanN + ibanC3 + ibanC4 + ibanC4 + ibanC,
	// Tunisia   (20n) TNkk bbss sccc cccc cccc cccc
	"TN": "(TN)" + ibanCK + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN4,
	// Turkey    (5n,17c)  TRkk bbbb bxcc cccc cccc cccc cc
	"TR": "(TR)" + ibanCK + ibanN4 + ibanN + ibanC3 + ibanC4 + ibanC4 + ibanC4 + ibanC2,
	// United Arab Emirates  (3n,16n)  AEkk bbbc cccc cccc cccc ccc
	"AE": "(AE)" + ibanCK + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN3,
	// United Kingdom (4a,14n) GBkk bbbb ssss sscc cccc cc
	"GB": "(GB)" + ibanCK + ibanA4 + ibanN4 + ibanN4 + ibanN4 + ibanN2,
	// Vatican City  (3n,15n)  VAkk bbbc cccc cccc cccc cc
	"VA": "(VA)" + ibanCK + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN2,
	// Virgin Islands, British   (4c,16n)  VGkk bbbb cccc cccc cccc cccc
	"VG": "(VG)" + ibanCK + ibanC4 + ibanN4 + ibanN4 + ibanN4 + ibanN4,
	// Egypt (25n) EGkk bbbb ssss cccc cccc cccc ccccc
	"EG": "(EG)" + ibanCK + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN,
	// Iraq  (4a,15n)  IQkk bbbb sssc cccc cccc ccc
	"IQ": "(IQ)" + ibanCK + ibanA4 + ibanN4 + ibanN4 + ibanN4 + ibanN3,
	// Libya (21n) LYkk bbbs ssss cccc cccc c
	"LY": "(LY)" + ibanCK + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN,
	// Saint Lucia   (4a,24c)  LCkk bbbb cccc cccc cccc cccc cccc cccc
	"LC": "(LC)" + ibanCK + ibanA4 + ibanC4 + ibanC4 + ibanC4 + ibanC4 + ibanC4 + ibanC4,
	// Seychelles    (4a,20n,3a)   SCkk bbbb ssnn cccc cccc cccc cccm mmm
	"SC": "(SC)" + ibanCK + ibanA4 + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanN4 + ibanA3,
	// Ukraine   (6n,19c)  UAkk bbbb bbcc cccc cccc cccc cccc c
	"UA": "(UA)" + ibanCK + ibanN4 + ibanN2 + ibanC4 + ibanC4 + ibanC4 + ibanC4 + ibanC3,
}
