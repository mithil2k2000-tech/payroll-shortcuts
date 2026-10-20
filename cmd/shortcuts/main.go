// Command shortcuts measures how far common simplifications of UK payroll arithmetic drift from HMRC's exact
// percentage method, on a synthetic workforce run through a full 2026-27 tax year.
//
// The reference is uk-payroll-engine (exact rational arithmetic, HMRC PAYE spec v24.0). Each shortcut changes
// exactly one thing, so its effect is isolated:
//
//	tax_float        HMRC's algorithm, but in float64 pounds
//	tax_no_t_floor   taxable pay to date not rounded down to whole pounds before the formula
//	tax_simple_bands band limits pro-rated as annual*n/periods, no 4-decimal truncation, no income-test ceiling
//	tax_no_quotient  free pay as ceil((code*10+9)/periods), without the quotient method for codes over 500
//	tax_round_near   final tax to date rounded to the nearest penny instead of down
//	tax_month1       every employee taxed on a Week 1/Month 1 (non-cumulative) basis
//	ni_half_up       NI rounded half-up (as math.Round) instead of half-down
//	ni_annual_div    NI thresholds taken as annual/periods instead of HMRC's published period figures
//	sl_round_pence   student loan rounded down to the penny instead of the whole pound
//
// Output: results/payslips.csv.gz is not written (too large); per-shortcut summaries go to results/summary.json
// and per-employee annual differences to results/annual.csv.
package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"sort"
	"strconv"

	"github.com/mithil2k2000-tech/uk-payroll-engine/money"
	"github.com/mithil2k2000-tech/uk-payroll-engine/ni"
	"github.com/mithil2k2000-tech/uk-payroll-engine/paye"
	"github.com/mithil2k2000-tech/uk-payroll-engine/studentloan"
	"github.com/mithil2k2000-tech/uk-payroll-engine/taxyear"
)

type employee struct {
	id      int
	freq    taxyear.Frequency
	code    string
	salary  float64 // annual, pounds
	pattern []float64
	niCat   string
	loan    string
}

var rules = taxyear.Latest()

var debugN int

func makeWorkforce(n int, rng *rand.Rand) []employee {
	out := make([]employee, n)
	for i := range out {
		e := employee{id: i, freq: taxyear.Monthly, niCat: "A"}
		if rng.Float64() < 0.2 {
			e.freq = taxyear.Weekly
		}
		// Lognormal annual pay, median about £37k (an approximation to UK full-time earnings).
		e.salary = math.Exp(math.Log(37000) + 0.55*rng.NormFloat64())
		if e.salary < 6000 {
			e.salary = 6000 + rng.Float64()*4000
		}
		r := rng.Float64()
		switch {
		case r < 0.80:
			e.code = "1257L"
		case r < 0.88:
			e.code = "S1257L"
		case r < 0.91:
			e.code = "C1257L"
		case r < 0.94:
			e.code = "BR"
		case r < 0.97:
			e.code = fmt.Sprintf("K%d", 50+rng.Intn(600))
		default:
			e.code = fmt.Sprintf("%dL", 600+rng.Intn(1400)) // other allowance codes, incl. under and over 1000
		}
		if rng.Float64() < 0.04 {
			e.code += " M1"
		}
		if rng.Float64() < 0.05 {
			e.niCat = "M" // under 21
		}
		if rng.Float64() < 0.03 {
			e.niCat = "C" // over State Pension age
		}
		lr := rng.Float64()
		switch {
		case lr < 0.15:
			e.loan = "2"
		case lr < 0.20:
			e.loan = "1"
		case lr < 0.23:
			e.loan = "5"
		case lr < 0.24:
			e.loan = "4"
		}
		periods := int(e.freq.PeriodsInYear())
		e.pattern = make([]float64, periods)
		vary := rng.Float64() < 0.3
		bonus := -1
		if rng.Float64() < 0.15 {
			bonus = rng.Intn(periods)
		}
		for p := range e.pattern {
			m := 1.0
			if vary {
				m += 0.1 * (rng.Float64() - 0.5)
			}
			if p == bonus {
				m += 1.0
			}
			e.pattern[p] = m
		}
		out[i] = e
	}
	return out
}

func pence(pounds float64) money.Pence { return money.Pence(math.Round(pounds * 100)) }

// ------------------------------------------------------------------ float PAYE shortcuts

type taxOpts struct {
	noTFloor, simpleBands, noQuotient, roundNear, month1 bool
}

func scaleFor(c paye.Country) taxyear.TaxScale {
	switch c {
	case paye.Scotland:
		return rules.Scotland
	case paye.Wales:
		return rules.Wales
	}
	return rules.England
}

func f(p money.Pence) float64 { return float64(p) / 100 }

// floatTax reimplements HMRC's method in float64 with the chosen simplifications. Returns tax to date in pounds.
func floatTax(code paye.TaxCode, freq taxyear.Frequency, n int64, payToDate float64, o taxOpts) float64 {
	periods := float64(freq.PeriodsInYear())
	s := scaleFor(code.Country)
	floorP := func(x float64) float64 { return math.Floor(x*100+1e-9) / 100 }
	ceilP := func(x float64) float64 { return math.Ceil(x*100-1e-9) / 100 }
	switch code.Kind {
	case paye.NoTax:
		return 0
	case paye.BasicRate, paye.DRate:
		rate := float64(s.BasicRate)
		if code.Kind == paye.DRate {
			rate = float64(s.DRates[code.DIndex])
		}
		if payToDate <= 0 {
			return 0
		}
		return floorP(math.Floor(payToDate) * rate / 100)
	}
	var perPeriod float64
	num := code.Number
	if num > 0 {
		val := func(x int64) float64 { return ceilP(float64(x*10+9) / periods) }
		if num <= 500 || o.noQuotient {
			perPeriod = val(num)
		} else {
			q, rem := (num-1)/500, (num-1)%500+1
			perPeriod = val(rem) + float64(q)*f(rules.QuotientStep.For(freq))
		}
	}
	free := perPeriod * float64(n)
	if code.Kind == paye.KCode {
		free = -free
	}
	u := payToDate - free
	if u <= 0 {
		return 0
	}
	t := math.Floor(u)
	if o.noTFloor {
		t = u
	}
	frac := float64(n) / periods
	prorate := func(annual float64) float64 {
		if o.simpleBands {
			return annual * frac
		}
		return math.Floor(annual*frac*10000+1e-7) / 10000
	}
	cum := s.CumulativeTax()
	lowerC, lowerK := 0.0, 0.0
	formula := func(k, c float64, rate int64) float64 {
		l := k + (t-c)*float64(rate)/100
		if o.roundNear {
			return math.Round(l*100) / 100
		}
		return floorP(l)
	}
	for i, b := range s.Bands {
		c := prorate(f(b.Upper))
		v := math.Ceil(c - 1e-9)
		if o.simpleBands {
			v = c
		}
		if u <= v {
			return formula(lowerK, lowerC, b.RatePercent)
		}
		lowerC, lowerK = c, prorate(f(cum[i]))
	}
	return formula(lowerK, lowerC, s.TopRate)
}

// ------------------------------------------------------------------ run

type stat struct {
	Payslips, Differ      int
	SumAbsPence, MaxAbs   int64
	AnnualDiffer, Annual1 int // employees whose annual total differs at all / by >= £1
	Employees             int
	MaxAnnualAbs          int64
	NetSigned             int64 // sum over employees of (shortcut - exact) annual, pence
}

func main() {
	nEmp := 50000
	if len(os.Args) > 1 {
		nEmp, _ = strconv.Atoi(os.Args[1])
	}
	rng := rand.New(rand.NewSource(2026))
	wf := makeWorkforce(nEmp, rng)
	names := []string{"tax_float", "tax_no_t_floor", "tax_simple_bands", "tax_no_quotient", "tax_round_near",
		"tax_month1", "ni_half_up", "ni_annual_div", "sl_round_pence"}
	opts := map[string]taxOpts{"tax_float": {}, "tax_no_t_floor": {noTFloor: true}, "tax_simple_bands": {simpleBands: true},
		"tax_no_quotient": {noQuotient: true}, "tax_round_near": {roundNear: true}, "tax_month1": {month1: true}}
	stats := map[string]*stat{}
	for _, nm := range names {
		stats[nm] = &stat{}
	}
	af, _ := os.Create("results/annual.csv")
	aw := csv.NewWriter(af)
	aw.Write(append([]string{"employee", "freq", "code", "ni", "loan", "salary", "exact_tax", "exact_ni", "exact_sl"}, names...))
	totalPayslips := 0
	for _, e := range wf {
		code, err := paye.ParseTaxCode(e.code)
		if err != nil {
			panic(err)
		}
		periods := e.freq.PeriodsInYear()
		var ytdPay, ytdTax money.Pence
		shortYTD := map[string]float64{} // float shortcuts: previous tax to date (pounds)
		annual := map[string]int64{}
		var exTax, exNI, exSL int64
		for p := int64(1); p <= periods; p++ {
			pay := pence(e.salary / float64(periods) * e.pattern[p-1])
			res, err := paye.Calculate(rules, paye.Input{Code: code, Frequency: e.freq, Period: p, TaxablePay: pay,
				PreviousTaxablePay: ytdPay, PreviousTax: ytdTax})
			if err != nil {
				panic(err)
			}
			nres, _ := ni.Calculate(rules, e.niCat, e.freq, pay)
			slres, _ := studentloan.Calculate(rules, e.loan, false, e.freq, pay)
			exTax += int64(res.Tax)
			exNI += int64(nres.Employee)
			exSL += int64(slres.StudentLoan)
			// PAYE shortcuts (the 50% regulatory limit is applied the same way as the engine).
			for nm, o := range opts {
				c := code
				n, ptd, prev := p, f(ytdPay+pay), shortYTD[nm]
				if o.month1 || !c.Cumulative {
					n, ptd, prev = 1, f(pay), 0
				}
				td := floatTax(c, e.freq, n, ptd, o)
				tax := td - prev
				if limit := math.Floor(f(pay)*50) / 100; tax > 0 && tax > limit {
					tax = limit
				}
				if !(o.month1 || !c.Cumulative) {
					shortYTD[nm] = prev + tax
				}
				tp := int64(math.Round(tax * 100))
				if os.Getenv("DEBUG") == nm && tp != int64(res.Tax) && debugN < 8 {
					debugN++
					fmt.Fprintf(os.Stderr, "%s code=%s freq=%s p=%d pay=%d ytdpay=%d exact=%d short=%d td=%.6f prev=%.6f exactYTDprev=%d\n",
						nm, e.code, e.freq, p, pay, ytdPay, res.Tax, tp, td, prev, ytdTax)
				}
				annual[nm] += tp
				record(stats[nm], tp-int64(res.Tax))
			}
			// NI shortcuts.
			rates := rules.NICategories[e.niCat]
			niShort := func(halfUp, annualDiv bool) int64 {
				per := float64(periods)
				th := func(pa taxyear.PeriodAmounts, ann float64) float64 {
					if annualDiv {
						return ann / per
					}
					return f(pa.For(e.freq))
				}
				pt, uel := th(rules.NI.PT, 12570), th(rules.NI.UEL, 50270)
				x := f(pay)
				ee := 0.0
				if x > pt {
					ee += (math.Min(x, uel) - pt) * float64(rates.EmployeeMain) / 100
				}
				if x > uel {
					ee += (x - uel) * float64(rates.EmployeeAbove) / 100
				}
				if halfUp {
					return int64(math.Round(ee * 100))
				}
				// half-down, as HMRC: subtract a hair before rounding
				return int64(math.Ceil(ee*100 - 0.5 - 1e-9))
			}
			for nm, args := range map[string][2]bool{"ni_half_up": {true, false}, "ni_annual_div": {false, true}} {
				v := niShort(args[0], args[1])
				annual[nm] += v
				record(stats[nm], v-int64(nres.Employee))
			}
			// Student loan rounded to the penny.
			slv := int64(0)
			if e.loan != "" {
				lp := rules.StudentLoans[e.loan]
				th := studentloan.PeriodThreshold(lp, e.freq)
				if pay > th {
					slv = int64(math.Floor(float64(pay-th) * float64(lp.RatePercent) / 100))
				}
			}
			annual["sl_round_pence"] += slv
			record(stats["sl_round_pence"], slv-int64(slres.StudentLoan))
			ytdPay += pay
			ytdTax += res.Tax
			totalPayslips++
		}
		row := []string{strconv.Itoa(e.id), string(e.freq), e.code, e.niCat, e.loan, fmt.Sprintf("%.0f", e.salary),
			strconv.FormatInt(exTax, 10), strconv.FormatInt(exNI, 10), strconv.FormatInt(exSL, 10)}
		for _, nm := range names {
			ref := exTax
			if nm[:2] == "ni" {
				ref = exNI
			} else if nm[:2] == "sl" {
				ref = exSL
			}
			d := annual[nm] - ref
			st := stats[nm]
			st.Employees++
			st.NetSigned += d
			if d != 0 {
				st.AnnualDiffer++
			}
			if abs(d) >= 100 {
				st.Annual1++
			}
			if abs(d) > st.MaxAnnualAbs {
				st.MaxAnnualAbs = abs(d)
			}
			row = append(row, strconv.FormatInt(d, 10))
		}
		aw.Write(row)
	}
	aw.Flush()
	af.Close()
	sort.Strings(names)
	b, _ := json.MarshalIndent(map[string]any{"employees": nEmp, "payslips": totalPayslips, "tax_year": rules.Name, "stats": stats}, "", " ")
	os.WriteFile("results/summary.json", b, 0o644)
	fmt.Println(string(b))
}

func record(s *stat, d int64) {
	s.Payslips++
	if d != 0 {
		s.Differ++
		s.SumAbsPence += abs(d)
		if abs(d) > s.MaxAbs {
			s.MaxAbs = abs(d)
		}
	}
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
