# Pennies and pounds: UK payroll shortcuts vs HMRC's exact method

📄 **Paper (PDF): [payroll-shortcuts.pdf](payroll-shortcuts.pdf)**

Differential testing of nine common simplifications of UK payroll arithmetic against an exact reference
([uk-payroll-engine](https://github.com/mithil2k2000-tech/uk-payroll-engine)), over 100,000 synthetic employees and
2,003,080 payslips in tax year 2026–27.

| Shortcut | Payslips differing | Employees ≥ £1 out at year end | Net over-deduction |
|---|---|---|---|
| PAYE in binary floating point | 0.24% | 0.00% | −£24 |
| Taxable pay not rounded down to whole pounds | 90.65% | 2.84% | £21,108 |
| No quotient method for free pay | 19.84% | 0.02% | £4,808 |
| Week 1/Month 1 basis for everyone | 50.34% | 72.93% | £1,925,374 |
| NI thresholds as annual/periods | 87.82% | 13.23% | £55,556 |
| Student loan rounded to the penny, not the pound | 16.04% | 15.59% | £159,610 |

Floating point mattered least: PAYE is cumulative and corrects itself. Threshold and rounding rules mattered most.

## Reproduce

```bash
git clone https://github.com/mithil2k2000-tech/uk-payroll-engine ../../uk-payroll-engine   # path used by go.mod's replace
go run ./cmd/shortcuts 100000      # under a minute
python src/analyse.py
cd paper && latexmk -pdf main.tex
```

## AI assistance

An AI assistant (Claude, Anthropic) helped write code and draft text. The author designed the study,
reviewed the code and results, and is responsible for the content.

## Licence

MIT
