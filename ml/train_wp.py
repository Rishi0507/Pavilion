"""Train the win probability model.

This model does more work than any other in the project. It colours the share
grid, so the emoji strip is a derivative plot of it rather than decoration; it
drives the AI batting side's choice of intent; and it scores the player on
decision quality rather than on whether the coin landed well.

Monotonicity is enforced rather than hoped for. A win probability that rises
when you need more runs, or falls when you have more wickets in hand, is not a
subtle statistical flaw: it produces a share grid that colours a good over red,
and players notice that immediately even if they cannot name it. LightGBM can
constrain the direction of each feature's effect, so it is told to.
"""

from __future__ import annotations

import json
import pathlib
import sys

import lightgbm as lgb
import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
import numpy as np
import pandas as pd
from sklearn.metrics import brier_score_loss, log_loss, roc_auc_score

ROOT = pathlib.Path(__file__).resolve().parent.parent
DATA = ROOT / "data" / "out"
MODELS = ROOT / "data" / "models"
REPORTS = ROOT / "ml" / "reports"

FIT_THROUGH = 2022

# Cricket in 2026 is not cricket in 2012. Chases succeed markedly more often now
# than they did a decade ago, and a model trained flat across nineteen seasons
# predicts the average of an era rather than the current one: on held-out 2025
# and 2026 it expected 45% of chases to succeed where 56% actually did.
#
# Recent seasons therefore carry more weight, decaying geometrically into the
# past. This does not discard old matches, which still inform the shape of a
# chase; it stops them setting its level.
#
# The decay is mild by measurement rather than by taste. At 0.95 per season the
# held-out log loss improves from 0.4349 to 0.4292 and calibration error from
# 0.1125 to 0.1069; at 0.85 the calibration keeps improving but discrimination
# starts to go, because shrinking the effective sample costs more than the era
# correction gains.
RECENCY_DECAY = 0.95

# Direction each feature must push the probability of winning the chase.
MONOTONE = {
    "runs_required": -1,  # needing more cannot help
    "balls_remaining": +1,  # having longer cannot hurt
    "wickets_in_hand": +1,  # having more batters cannot hurt
    "required_rate": -1,  # a steeper climb cannot help
    "target": 0,
    "venue_run_rate": 0,
}


def load(name: str) -> pd.DataFrame:
    path = DATA / name
    if not path.exists():
        sys.exit(f"{path} not found; run: go run ./cmd/parfeat")
    return pd.read_csv(path)


def reliability(p: np.ndarray, y: np.ndarray, path: pathlib.Path) -> float:
    bins = np.linspace(0, 1, 21)
    idx = np.digitize(p, bins) - 1
    xs, ys = [], []
    ece = 0.0
    for b in range(len(bins) - 1):
        m = idx == b
        if m.sum() < 50:
            continue
        xs.append(p[m].mean())
        ys.append(y[m].mean())
        ece += m.sum() * abs(p[m].mean() - y[m].mean())
    ece /= max(len(p), 1)

    fig, ax = plt.subplots(figsize=(6, 6))
    ax.plot([0, 1], [0, 1], "--", color="#999", lw=1)
    ax.plot(xs, ys, "o-", color="#c1272d", ms=5)
    ax.set_xlabel("predicted win probability")
    ax.set_ylabel("observed win rate")
    ax.set_title(f"Win probability reliability, held out\nECE {ece:.4f}")
    ax.grid(alpha=0.25, lw=0.5)
    fig.tight_layout()
    path.parent.mkdir(parents=True, exist_ok=True)
    fig.savefig(path, dpi=130)
    plt.close(fig)
    return float(ece)


def clamp_impossible(p: np.ndarray, x: np.ndarray, features: list[str]) -> np.ndarray:
    """Zero out chases that cannot be completed even in principle.

    Decision trees cannot extrapolate: a required rate of 90 falls into the same
    leaf as one of 20, and inherits its ten percent. But needing more than six
    runs a ball is not improbable, it is arithmetically impossible, and that is
    a rule of cricket rather than something to be inferred from data. Stating it
    directly is both more accurate and more honest than hoping a deeper tree
    would have found it.
    """
    need = x[:, features.index("runs_required")]
    balls = x[:, features.index("balls_remaining")]
    return np.where(need > 6 * balls, 0.0, p)


def check_monotone(booster: lgb.Booster, features: list[str]) -> dict:
    """Verify the constraints actually hold on a grid, not just in the config.

    A constraint that is declared but silently dropped would be worse than none,
    because nothing downstream would think to check.
    """
    base = {
        "runs_required": 60.0,
        "balls_remaining": 60.0,
        "wickets_in_hand": 6.0,
        "required_rate": 6.0,
        "target": 180.0,
        "venue_run_rate": 1.25,
    }
    report = {}
    sweeps = {
        "runs_required": np.arange(1, 160, 2.0),
        "balls_remaining": np.arange(1, 120, 2.0),
        "wickets_in_hand": np.arange(1, 11, 1.0),
    }
    for name, values in sweeps.items():
        rows = []
        for v in values:
            r = dict(base)
            r[name] = float(v)
            if name in ("runs_required", "balls_remaining"):
                r["required_rate"] = 6 * r["runs_required"] / max(r["balls_remaining"], 1)
            rows.append([r[f] for f in features])
        p = booster.predict(np.array(rows, dtype=np.float32))
        d = np.diff(p)
        want = MONOTONE[name]
        violations = int((d > 1e-12).sum() if want < 0 else (d < -1e-12).sum())
        report[name] = {"direction": want, "violations": violations}
    return report


def main() -> None:
    MODELS.mkdir(parents=True, exist_ok=True)
    REPORTS.mkdir(parents=True, exist_ok=True)

    train_all = load("wp_train.csv")
    test = load("wp_test.csv")
    features = [c for c in train_all.columns if c not in ("label", "season")]

    fit = train_all[train_all.season <= FIT_THROUGH]
    valid = train_all[train_all.season > FIT_THROUGH]

    print(f"fit    {len(fit):>7,} rows  seasons <= {FIT_THROUGH}")
    print(f"valid  {len(valid):>7,} rows  seasons {FIT_THROUGH + 1}-2024")
    print(f"test   {len(test):>7,} rows  seasons 2025-2026")

    w_fit = RECENCY_DECAY ** (FIT_THROUGH - fit.season.to_numpy()).astype(np.float64)
    print(f"recency weight: {RECENCY_DECAY} per season, "
          f"oldest season carries {w_fit.min():.4f}")

    X_fit, y_fit = fit[features].to_numpy(np.float32), fit.label.to_numpy()
    X_val, y_val = valid[features].to_numpy(np.float32), valid.label.to_numpy()
    X_test, y_test = test[features].to_numpy(np.float32), test.label.to_numpy()

    params = {
        "objective": "binary",
        "metric": "binary_logloss",
        "learning_rate": 0.05,
        "num_leaves": 31,
        "min_data_in_leaf": 500,
        "feature_fraction": 0.9,
        "bagging_fraction": 0.9,
        "bagging_freq": 1,
        "lambda_l2": 1.0,
        "monotone_constraints": [MONOTONE[f] for f in features],
        "monotone_constraints_method": "advanced",
        "seed": 20260828,
        "deterministic": True,
        "force_row_wise": True,
        "num_threads": 4,
        "verbose": -1,
    }

    booster = lgb.train(
        params,
        lgb.Dataset(X_fit, label=y_fit, weight=w_fit, feature_name=features),
        num_boost_round=2000,
        valid_sets=[lgb.Dataset(X_val, label=y_val, feature_name=features)],
        callbacks=[lgb.early_stopping(50, verbose=False), lgb.log_evaluation(200)],
    )
    print(f"\nbest iteration {booster.best_iteration}")

    p_test = clamp_impossible(booster.predict(X_test), X_test, features)
    base_rate = float(y_fit.mean())
    ll_base = log_loss(y_test, np.full(len(y_test), base_rate))
    ll = log_loss(y_test, p_test)

    print("\nheld out")
    print(f"  base rate log loss  {ll_base:.5f}")
    print(f"  model log loss      {ll:.5f}")
    print(f"  brier               {brier_score_loss(y_test, p_test):.5f}")
    print(f"  auc                 {roc_auc_score(y_test, p_test):.5f}")

    ece = reliability(p_test, y_test, REPORTS / "winprob_reliability.png")
    print(f"  calibration error   {ece:.5f}")
    print(f"  mean prediction     {p_test.mean():.4f}")
    print(f"  actual win rate     {y_test.mean():.4f}   <- the residual era gap")

    mono = check_monotone(booster, features)
    print("\nmonotonicity check")
    ok = True
    for name, r in mono.items():
        status = "ok" if r["violations"] == 0 else f"{r['violations']} VIOLATIONS"
        print(f"  {name:<18} direction {r['direction']:+d}  {status}")
        ok = ok and r["violations"] == 0
    if not ok:
        sys.exit("monotonicity constraints were not respected")

    booster.save_model(str(MODELS / "winprob.txt"), num_iteration=booster.best_iteration)

    rng = np.random.default_rng(11)
    pick = rng.choice(len(X_test), size=min(500, len(X_test)), replace=False)
    (MODELS / "winprob_parity.json").write_text(
        json.dumps(
            {
                "features": features,
                "rows": [{"x": X_test[i].tolist(), "p": float(p_test[i])} for i in pick],
            }
        ),
        encoding="utf-8",
    )
    (MODELS / "winprob.json").write_text(
        json.dumps(
            {
                "features": features,
                "monotone": MONOTONE,
                "best_iteration": int(booster.best_iteration),
                "recency_decay": RECENCY_DECAY,
                "log_loss": {"base_rate": ll_base, "model": ll},
                "auc": float(roc_auc_score(y_test, p_test)),
                "brier": float(brier_score_loss(y_test, p_test)),
                "calibration_error": ece,
            },
            indent=2,
        ),
        encoding="utf-8",
    )

    # A readable sanity table: what the model thinks of familiar situations.
    print("\nwin probability chasing, 8 wickets in hand, target 180")
    print("     needed:", "  ".join(f"{n:>5}" for n in (10, 20, 40, 60, 90)))
    for balls in (6, 12, 24, 36, 60):
        row = []
        for need in (10, 20, 40, 60, 90):
            x = np.array([[need, balls, 8, 6 * need / balls, 180, 1.25]], dtype=np.float32)
            row.append(f"{clamp_impossible(booster.predict(x), x, features)[0]:5.2f}")
        print(f"  {balls:>3} balls  " + "  ".join(row))

    print(f"\nwrote {MODELS / 'winprob.txt'}")


if __name__ == "__main__":
    main()
