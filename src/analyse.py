"""Table and figure for the payroll-shortcuts paper."""
import json
from pathlib import Path

import matplotlib
import pandas as pd

matplotlib.use("Agg")
import matplotlib.pyplot as plt  # noqa: E402

ROOT = Path(__file__).resolve().parent.parent
d = json.loads((ROOT / "results" / "summary.json").read_text())
LABEL = {
    "tax_float": ("PAYE", "Exact algorithm in binary floating point"),
    "tax_simple_bands": ("PAYE", "Band limits pro-rated without truncation or ceiling"),
    "tax_no_t_floor": ("PAYE", "Taxable pay not rounded down to whole pounds"),
    "tax_no_quotient": ("PAYE", "Free pay without the quotient method"),
    "tax_round_near": ("PAYE", "Tax rounded to nearest penny, not down"),
    "tax_month1": ("PAYE", "Week 1/Month 1 basis for everyone"),
    "ni_half_up": ("NI", "Half a penny rounded up, not down"),
    "ni_annual_div": ("NI", "Thresholds as annual/periods, not HMRC's figures"),
    "sl_round_pence": ("Student loan", "Rounded down to the penny, not the pound"),
}
rows = []
for k, (area, lab) in LABEL.items():
    s = d["stats"][k]
    rows.append({"Shortcut": f"{area}: {lab}",
                 "Slips $\\neq$": f"{s['Differ'] / s['Payslips'] * 100:.2f}\\%",
                 "Max/slip": f"\\pounds{s['MaxAbs'] / 100:.2f}",
                 "Staff $\\neq$": f"{s['AnnualDiffer'] / s['Employees'] * 100:.2f}\\%",
                 "Staff $\\geq$\\pounds1": f"{s['Annual1'] / s['Employees'] * 100:.2f}\\%",
                 "Max/year": f"\\pounds{s['MaxAnnualAbs'] / 100:.2f}",
                 "Net": f"{'$-$' if s['NetSigned'] < 0 else ''}\\pounds{abs(s['NetSigned']) / 100:,.0f}"})
t = pd.DataFrame(rows)
(ROOT / "paper" / "tables" / "main.tex").write_text(
    "\\begin{table*}[t]\n\\centering\n\\caption{Differences from HMRC's exact method for " + f"{d['employees']:,} employees and "
    f"{d['payslips']:,} payslips over tax year {d['tax_year']}" + ". Each shortcut changes one rule. Net is the sum over all "
    "employees of (shortcut $-$ exact) annual deductions; positive means over-deduction.}\n\\label{tab:main}\n\\footnotesize\n"
    "\\setlength{\\tabcolsep}{3pt}\n" + t.to_latex(index=False, escape=False, column_format="p{6.2cm}rrrrrr") + "\\end{table*}\n")

fig, ax = plt.subplots(figsize=(5.6, 2.6))
plt.rcParams.update({"font.size": 8, "font.family": "serif"})
keys = list(LABEL)[::-1]
a = [d["stats"][k]["AnnualDiffer"] / d["stats"][k]["Employees"] * 100 for k in keys]
b = [d["stats"][k]["Annual1"] / d["stats"][k]["Employees"] * 100 for k in keys]
y = range(len(keys))
ax.barh([i + 0.2 for i in y], a, 0.4, color="#9ecae1", label="annual total differs")
ax.barh([i - 0.2 for i in y], b, 0.4, color="#1f4e79", label="differs by \u00a31 or more")
ax.set_yticks(list(y))
ax.set_yticklabels([LABEL[k][1] for k in keys], fontsize=6)
ax.set_xlabel("Share of employees (%)")
ax.legend(fontsize=6, frameon=False, loc="lower right")
ax.spines[["top", "right"]].set_visible(False)
fig.tight_layout()
fig.savefig(ROOT / "paper" / "figures" / "employees.pdf")
print(t.to_string())
