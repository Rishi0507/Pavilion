"""Train and calibrate the ball outcome model.

The model is a LightGBM multiclass classifier over the nine outcomes a delivery
can produce. Features are computed in Go by internal/features and exported by
cmd/parfeat, so there is one feature implementation rather than two and no
opportunity for training and serving to drift apart.

Three time-ordered splits, never shuffled, because shuffling a time series lets
the model learn from matches that had not happened yet:

    fit    seasons up to 2022   -- what the trees are grown on
    valid  2023 to 2024         -- early stopping and temperature fitting
    test   2025 to 2026         -- touched exactly once, at the end

Calibration is temperature scaling on the raw margins. Isotonic regression per
class would break the simplex and need renormalising, which is both harder to
justify and harder to reimplement exactly in Go; a single temperature preserves
the ranking, keeps the outputs a valid distribution, and ports to eight lines of
Go that the parity test can check to 1e-6.
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
from scipy.optimize import minimize_scalar
from sklearn.metrics import log_loss

ROOT = pathlib.Path(__file__).resolve().parent.parent
DATA = ROOT / "data" / "out"
MODELS = ROOT / "data" / "models"
REPORTS = ROOT / "ml" / "reports"

OUTCOMES = ["0", "1", "2", "3", "4", "6", "W", "wd", "nb"]
NUM_CLASS = len(OUTCOMES)

FIT_THROUGH = 2022
VALID_THROUGH = 2024


def load(name: str) -> pd.DataFrame:
    path = DATA / name
    if not path.exists():
        sys.exit(f"{path} not found; run: go run ./cmd/parfeat")
    return pd.read_csv(path)


def softmax(z: np.ndarray) -> np.ndarray:
    z = z - z.max(axis=1, keepdims=True)
    e = np.exp(z)
    return e / e.sum(axis=1, keepdims=True)


def fit_temperature(margins: np.ndarray, y: np.ndarray) -> float:
    """Find the single scalar that best calibrates the raw margins.

    An uncalibrated boosted model is usually overconfident: it has been trained
    to separate classes, not to report honest probabilities. Dividing the
    margins by a temperature above one softens every prediction by the same
    amount, which is enough to fix most of the miscalibration without touching
    the ordering.
    """

    def nll(log_t: float) -> float:
        p = softmax(margins / np.exp(log_t))
        return log_loss(y, np.clip(p, 1e-15, 1), labels=list(range(NUM_CLASS)))

    res = minimize_scalar(nll, bounds=(-2.0, 2.0), method="bounded")
    return float(np.exp(res.x))


def reliability(p: np.ndarray, y: np.ndarray, title: str, path: pathlib.Path) -> dict:
    """Plot a reliability diagram per outcome and return the calibration error.

    A model can have a fine log loss and still be systematically overconfident.
    The diagram is the only way to see it: perfectly calibrated predictions lie
    on the diagonal, and a curve sagging below it means the model claims more
    certainty than it has earned.
    """
    fig, axes = plt.subplots(3, 3, figsize=(11, 10), sharex=True, sharey=True)
    bins = np.linspace(0, 1, 11)
    ece_per_class = {}

    for k, ax in enumerate(axes.flat):
        if k >= NUM_CLASS:
            ax.axis("off")
            continue
        pk = p[:, k]
        yk = (y == k).astype(float)
        idx = np.digitize(pk, bins) - 1
        xs, ys, ws = [], [], []
        ece = 0.0
        for b in range(len(bins) - 1):
            m = idx == b
            if m.sum() < 20:
                continue
            xs.append(pk[m].mean())
            ys.append(yk[m].mean())
            ws.append(m.sum())
            ece += m.sum() * abs(pk[m].mean() - yk[m].mean())
        ece /= max(len(pk), 1)
        ece_per_class[OUTCOMES[k]] = float(ece)

        ax.plot([0, 1], [0, 1], "--", color="#999", lw=1)
        if xs:
            hi = max(max(xs), max(ys)) * 1.15
            ax.plot(xs, ys, "o-", color="#c1272d", ms=4, lw=1.4)
            ax.set_xlim(0, hi)
            ax.set_ylim(0, hi)
        ax.set_title(f"{OUTCOMES[k]}   ECE {ece:.4f}", fontsize=10)
        ax.grid(alpha=0.25, lw=0.5)

    fig.suptitle(title, fontsize=13)
    fig.supxlabel("predicted probability")
    fig.supylabel("observed frequency")
    fig.tight_layout()
    path.parent.mkdir(parents=True, exist_ok=True)
    fig.savefig(path, dpi=130)
    plt.close(fig)
    return ece_per_class


def main() -> None:
    MODELS.mkdir(parents=True, exist_ok=True)
    REPORTS.mkdir(parents=True, exist_ok=True)

    train_all = load("train.csv")
    test = load("test.csv")
    feature_names = [c for c in train_all.columns if c not in ("label", "season")]

    fit = train_all[train_all.season <= FIT_THROUGH]
    valid = train_all[train_all.season > FIT_THROUGH]

    print(f"features {len(feature_names)}")
    print(f"fit    {len(fit):>7,} rows  seasons <= {FIT_THROUGH}")
    print(f"valid  {len(valid):>7,} rows  seasons {FIT_THROUGH + 1}-{VALID_THROUGH}")
    print(f"test   {len(test):>7,} rows  seasons > {VALID_THROUGH}")

    X_fit, y_fit = fit[feature_names].to_numpy(np.float32), fit.label.to_numpy()
    X_val, y_val = valid[feature_names].to_numpy(np.float32), valid.label.to_numpy()
    X_test, y_test = test[feature_names].to_numpy(np.float32), test.label.to_numpy()

    params = {
        "objective": "multiclass",
        "num_class": NUM_CLASS,
        "metric": "multi_logloss",
        "learning_rate": 0.05,
        "num_leaves": 63,
        "min_data_in_leaf": 200,
        "feature_fraction": 0.85,
        "bagging_fraction": 0.85,
        "bagging_freq": 1,
        "lambda_l2": 1.0,
        # Determinism: the artifact must be reproducible from raw data.
        "seed": 20260828,
        "deterministic": True,
        "force_row_wise": True,
        "num_threads": 4,
        "verbose": -1,
    }

    booster = lgb.train(
        params,
        lgb.Dataset(X_fit, label=y_fit, feature_name=feature_names),
        num_boost_round=2000,
        valid_sets=[lgb.Dataset(X_val, label=y_val, feature_name=feature_names)],
        callbacks=[lgb.early_stopping(50, verbose=False), lgb.log_evaluation(100)],
    )
    print(f"\nbest iteration {booster.best_iteration}")

    # Baseline: the population outcome distribution, ignoring every feature.
    # A model that cannot beat this is not worth serving.
    base = np.bincount(y_fit, minlength=NUM_CLASS) / len(y_fit)
    base_test = np.tile(base, (len(y_test), 1))

    m_val = booster.predict(X_val, raw_score=True)
    m_test = booster.predict(X_test, raw_score=True)

    temperature = fit_temperature(m_val, y_val)
    print(f"fitted temperature {temperature:.4f}")

    p_test_raw = softmax(m_test)
    p_test_cal = softmax(m_test / temperature)

    ll_base = log_loss(y_test, base_test, labels=list(range(NUM_CLASS)))
    ll_raw = log_loss(y_test, p_test_raw, labels=list(range(NUM_CLASS)))
    ll_cal = log_loss(y_test, p_test_cal, labels=list(range(NUM_CLASS)))

    print("\nheld-out log loss")
    print(f"  population baseline  {ll_base:.5f}")
    print(f"  model, uncalibrated  {ll_raw:.5f}")
    print(f"  model, calibrated    {ll_cal:.5f}")

    ece_raw = reliability(
        p_test_raw, y_test, "Reliability before calibration (held out)", REPORTS / "reliability_raw.png"
    )
    ece_cal = reliability(
        p_test_cal, y_test, "Reliability after temperature scaling (held out)", REPORTS / "reliability_calibrated.png"
    )
    print("\nexpected calibration error by outcome")
    for k in OUTCOMES:
        print(f"  {k:>3}  {ece_raw[k]:.5f} -> {ece_cal[k]:.5f}")

    booster.save_model(str(MODELS / "outcome.txt"), num_iteration=booster.best_iteration)

    # Parity fixtures: a sample of held-out rows with the probabilities this
    # exact model produced. The Go implementation is checked against them, so a
    # divergence is caught the moment it appears rather than in a live game.
    rng = np.random.default_rng(7)
    pick = rng.choice(len(X_test), size=min(500, len(X_test)), replace=False)
    fixtures = {
        "features": feature_names,
        "outcomes": OUTCOMES,
        "temperature": temperature,
        "rows": [
            {"x": X_test[i].tolist(), "p": p_test_cal[i].tolist()} for i in pick
        ],
    }
    (MODELS / "parity.json").write_text(json.dumps(fixtures), encoding="utf-8")

    meta = {
        "features": feature_names,
        "outcomes": OUTCOMES,
        "temperature": temperature,
        "best_iteration": int(booster.best_iteration),
        "num_class": NUM_CLASS,
        "fit_through_season": FIT_THROUGH,
        "valid_through_season": VALID_THROUGH,
        "log_loss": {"baseline": ll_base, "uncalibrated": ll_raw, "calibrated": ll_cal},
        "ece_before": ece_raw,
        "ece_after": ece_cal,
    }
    (MODELS / "outcome.json").write_text(json.dumps(meta, indent=2), encoding="utf-8")

    gain = booster.feature_importance("gain")
    order = np.argsort(gain)[::-1]
    print("\ntop features by gain")
    for i in order[:12]:
        print(f"  {feature_names[i]:<22} {gain[i] / gain.sum() * 100:5.2f}%")

    print(f"\nwrote {MODELS / 'outcome.txt'}")
    print(f"wrote {MODELS / 'outcome.json'}")
    print(f"wrote {MODELS / 'parity.json'}")
    print(f"wrote {REPORTS / 'reliability_calibrated.png'}")


if __name__ == "__main__":
    main()
