#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""paired_eval.py — harness for the single-mode rewrite paired behavioral eval.

Implements the Task 15 fold-and-measure gate harness (spec §7) as adjudicated
2026-11-11 (P-MIN-R with grafts). This file is HARNESS ONLY:

  * `list` / `prepare` / `preflight` never invoke any model or `claude`
    binary (verified with a PATH stub that fails the test if executed).
  * `run` / `judge` REFUSE without a user-filled tolerances file and a budget
    statement. Actual model runs happen only after the user approves a budget
    at a real checkpoint; `run` executes against a stub runner
    (`PAIRED_EVAL_CLAUDE_CMD`) for plumbing selftests.

Layout per case×arm (adjudicated):

  $CONTROL = evals/paired/results/<run_id>/     (trusted side; never actor-read)
      truth.json, meta.yaml            frozen case input copies
      gate-policy.yaml, canary.txt     pinned gate policy + contamination canary
      run-manifest.json                refs, digests, env, channels, tier
      pristine-repo/                   pre-actor tree snapshot (judge anchors)
      probes.json, gate-records.json   isolation + gate evidence
      arm-archive.json, run-record.json, run-status.json, judge/
  $RUN = <tmp>/summoner-paired-eval/<case>-<arm>-<stamp>/     (actor side)
      repo/     fixture + setup patch + per-arm manifest (no truth ever)
      plugin/   staged plugin copy (allowlist; corpus-free; scripts/ kept)
      state/{s,t,m}   Summoner runtime state (write via env; READ DENIED)
      out/      transcripts, probe transcripts, actor outputs

Environment overrides for tests: PAIRED_EVAL_CLAUDE_CMD (stub runner —
contract: it receives the exact argv/cwd/env and writes the stream-json
transcript to $PAIRED_EVAL_TRANSCRIPT_PATH), PAIRED_EVAL_GATE_POLICY (an
alternate gate-policy.yaml path, used consistently by prepare+run),
PAIRED_EVAL_FORCE_G4_CONTINGENCY=1 (selftest the contingency branch).
"""

import argparse
import hashlib
import io
import json
import os
import platform
import re
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile
import time

# --------------------------------------------------------------------------
# Constants
# --------------------------------------------------------------------------

HARNESS_DIR = os.path.dirname(os.path.abspath(__file__))
EVALS_DIR = os.path.dirname(HARNESS_DIR)                     # evals/paired
REPO_ROOT = os.path.dirname(os.path.dirname(EVALS_DIR))
CASES_DIR = os.path.join(EVALS_DIR, "cases")
RESULTS_DEFAULT = os.path.join(EVALS_DIR, "results")
MANIFEST_DIGEST = os.path.join(HARNESS_DIR, "MANIFEST.digest")
V2_MANIFEST = os.path.join(HARNESS_DIR, "manifests", "summoner.v2.yaml")
EXPECTED_DELTAS = os.path.join(HARNESS_DIR, "expected-deltas.yaml")
TOLERANCES_EXAMPLE = os.path.join(EVALS_DIR, "tolerances.yaml.example")
WORKER_AGENT_COPY = os.path.join(EVALS_DIR, "agents", "worker.yaml")
SHIPPED_WORKER_MD = os.path.join(REPO_ROOT, "agents", "worker.md")
BASELINE_TAG = "v0.3.0"
TMP_ROOT_DEFAULT = os.path.join(tempfile.gettempdir(), "summoner-paired-eval")

# Staging allowlist (identical for both arms; adjudicated). scripts/ is
# REQUIRED — v0.3.0 skills invoke ${CLAUDE_PLUGIN_ROOT}/scripts/… (e.g.
# init-memory-db.sh). Everything not listed is dropped, notably the corpus
# (evals/), scorers/, tests/, memory/, .git, .superpowers, .claude/.
STAGE_ALLOWLIST = [
    "plugin.json", ".claude-plugin", "skills", "commands", "agents",
    "references", "hooks", "summoner-ctx", "scripts", "docs",
    "settings-recommended.json",
]

# Harness entry scripts live under the allowlisted scripts/ path once
# committed, but they are EVAL infrastructure, never plugin runtime —
# test-paired-eval-harness.sh embeds expected case outcomes, so staging it
# would leak answers (P4 forbids its name in actor roots for exactly this
# reason). They are excluded from every staged plugin tree, both arms.
STAGE_EXCLUDE = ["scripts/run-paired-eval.sh", "scripts/test-paired-harness.sh"]

# Actor workspace must never contain these corpus/harness file names (P4).
FORBIDDEN_ACTOR_NAMES = {
    "truth.json", "meta.yaml", "setup.patch", "generate.py", "task.md",
    "MANIFEST.digest", "expected-deltas.yaml", "gate-policy.yaml",
    "tolerances.yaml", "tolerances.yaml.example", "profile.sbpl",
    "paired_eval.py", "test-paired-harness.sh", "run-paired-eval.sh",
    "SHA256SUMS", "producer-prompt.md",
}

# Fixture git identity: generic author, per-command isolated config only.
FIXTURE_USER_NAME = "summoner-eval-fixture"
FIXTURE_USER_EMAIL = "fixture@summoner.invalid"
FIXTURE_COMMIT_MSG = "pristine fixture (pre-patch, harness-prepared)"

ENV_STATE = "SUMMONER_STATE_DIR"
ENV_TRACE = "SUMMONER_TRACE_DIR"
ENV_MEMORY = "SUMMONER_MEMORY_DIR"
ENV_ENFORCE = "SUMMONER_HOOKS_ENFORCE"

# ---------------------------------------------------------------------------
# Group / arm table (adjudicated wiring). Every case has exactly two arms in
# the default matrix; g3-prime is an optional, user-budget-permitting pair.
# required_tier: "P9" = execution-needing arm (Bash-child confinement must
# hold); "permission" = no-Bash arm at the lower tier, disclosed as a metric
# column. channel: candidate gates are AskUserQuestion dialogs (the only
# decision channel); baseline v0.3.0 gates are chat prose.
# ---------------------------------------------------------------------------

ARM_TABLE = {
    1: [
        {"arm": "baseline", "ref": "tag:" + BASELINE_TAG,
         "label": "v0.3.0 /summoner:fix — per-node ⑤ + role orchestration",
         "strategy": "per-node review + role orchestration",
         "manifest": "frozen-v1", "channel": "chat_prose",
         "required_tier": "P9", "tier_reason": "fix arm executes go build/test"},
        {"arm": "candidate", "ref": "commit:HEAD",
         "label": "candidate fix — main loop + terminal ⑤ + attended acceptance gate",
         "strategy": "single main loop + terminal review + human acceptance gate",
         "manifest": "harness-v2", "channel": "askuserquestion",
         "required_tier": "P9", "tier_reason": "fix arm executes go build/test",
         "acceptance_gate_human_only": True},
    ],
    2: [
        {"arm": "baseline", "ref": "tag:" + BASELINE_TAG,
         "label": "v0.3.0 /summoner:ship — persona fan-out + merge",
         "strategy": "multi-persona fan-out + merge",
         "manifest": "frozen-v1", "channel": "chat_prose",
         "required_tier": "permission", "tier_reason": "review arm; static evidence suffices (disclosed)"},
        {"arm": "candidate", "ref": "commit:HEAD",
         "label": "candidate /summoner:ship — single rubric reviewer (verification_spec)",
         "strategy": "single rubric reviewer over REVIEW-CRITERIA.md",
         "manifest": "harness-v2", "channel": "askuserquestion",
         "required_tier": "permission", "tier_reason": "review arm; static evidence suffices (disclosed)"},
    ],
    3: [
        {"arm": "baseline", "ref": "tag:" + BASELINE_TAG,
         "label": "v0.3.0 — genuinely independent parallel shards (store/api/tests)",
         "strategy": "parallel shards (corpus-designed, version-entangled — DISCLOSED)",
         "manifest": "frozen-v1", "channel": "chat_prose",
         "required_tier": "P9", "tier_reason": "rename task runs go build/test"},
        {"arm": "candidate", "ref": "commit:HEAD",
         "label": "candidate — single-loop sequential execution",
         "strategy": "sequential single loop (corpus-designed counterpart)",
         "manifest": "harness-v2", "channel": "askuserquestion",
         "required_tier": "P9", "tier_reason": "rename task runs go build/test"},
    ],
    4: [
        {"arm": "worker", "ref": "commit:HEAD",
         "label": "candidate + isolated reading worker (session-scoped --agents file)",
         "strategy": "isolated reading worker via --agents (byte-pinned worker.md)",
         "manifest": "harness-v2", "channel": "askuserquestion",
         "required_tier": "P9", "tier_reason": "audit + repair arm executes go test",
         "agents_file": True},
        {"arm": "folded", "ref": "commit:HEAD",
         "label": "candidate, Agent denied — reading folded into main context",
         "strategy": "folded reading (Agent tool denied)",
         "manifest": "harness-v2", "channel": "askuserquestion",
         "required_tier": "P9", "tier_reason": "audit + repair arm executes go test",
         "deny_agent": True},
    ],
    5: [
        {"arm": "independent", "ref": "commit:HEAD",
         "label": "candidate, fresh session — independent ⑤ (new context/evidence)",
         "strategy": "independent review: fresh session, producer framing absent",
         "manifest": "harness-v2", "channel": "askuserquestion",
         "required_tier": "permission", "tier_reason": "static review (g5-a raises to P9: Limit=0 probe)"},
        {"arm": "self-review", "ref": "commit:HEAD",
         "label": "candidate, --resume producer --fork-session — same-context self-review",
         "strategy": "self-review: fork of the seeded producer session (P8 allowance)",
         "manifest": "harness-v2", "channel": "askuserquestion",
         "required_tier": "permission", "tier_reason": "static review (g5-a raises to P9: Limit=0 probe)",
         "fork_from_producer": True},
    ],
}

PRODUCER_ARM = {"arm": "producer", "ref": "commit:HEAD",
                "label": "seeded producer (adoption only; NO verification instruction)",
                "strategy": "seeded producer", "manifest": "harness-v2",
                "channel": "none", "required_tier": "permission",
                "tier_reason": "producer adoption; no gates"}

G3_PRIME_ARMS = [
    {"arm": "delegate", "ref": "commit:HEAD",
     "label": "candidate-only g3-prime — delegation to the worker",
     "strategy": "delegation via worker (optional pair, user-budget-permitting)",
     "manifest": "harness-v2", "channel": "askuserquestion",
     "required_tier": "P9", "tier_reason": "rename task runs go build/test",
     "agents_file": True, "optional": True},
    {"arm": "sequential", "ref": "commit:HEAD",
     "label": "candidate-only g3-prime — sequential baseline of the same task",
     "strategy": "sequential (optional pair counterpart)",
     "manifest": "harness-v2", "channel": "askuserquestion",
     "required_tier": "P9", "tier_reason": "rename task runs go build/test",
     "optional": True},
]

# Case-level tier overrides (adjudication: g5-a's Limit=0 probe is
# execution-needing, so BOTH topologies on that case require the P9 tier).
TIER_OVERRIDES = {("g5-a", "independent"): "P9", ("g5-a", "self-review"): "P9"}

GROUP_TITLES = {
    1: "① workflow review (fix): per-node ⑤+roles vs main-loop+terminal ⑤+human gate",
    2: "② pre-release review (ship): multi-persona vs single rubric reviewer",
    3: "③ sharding: parallel shards vs sequential (corpus-designed, disclosed)",
    4: "④ context size: isolated reading worker vs folded reading",
    5: "⑤ review independence: independent ⑤ vs same-context self-review",
}

IS_DARWIN = sys.platform == "darwin"

# ---------------------------------------------------------------------------
# Small utilities
# ---------------------------------------------------------------------------


def plugin_binary(plugin_dir, name):
    """Resolve a staged plugin's hook binary for THIS host. The unsuffixed
    tree copy is whatever OS cut the release (v0.3.0 shipped darwin-x86_64 —
    the 2026-11-17 v0.5.0 release run died on a linux runner exec'ing it), so
    prefer this platform's suffixed variant when the staged plugin ships one
    and only then fall back to the unsuffixed copy. Never invents a path."""
    suffix = None
    machine = platform.machine()
    if IS_DARWIN:
        suffix = "darwin-arm64" if machine == "arm64" else "darwin-amd64"
    elif sys.platform.startswith("linux"):
        suffix = "linux-arm64" if machine in ("aarch64", "arm64") else "linux-amd64"
    if suffix:
        candidate = os.path.join(plugin_dir, "hooks", "bin", "%s-%s" % (name, suffix))
        if os.path.isfile(candidate):
            return candidate
    return os.path.join(plugin_dir, "hooks", "bin", name)


def die(msg, code=2):
    sys.stderr.write("paired-eval: %s\n" % msg)
    sys.exit(code)


def info(msg):
    sys.stderr.write("paired-eval: %s\n" % msg)


def sha256_bytes(data):
    return hashlib.sha256(data).hexdigest()


def sha256_file(path):
    h = hashlib.sha256()
    with open(path, "rb") as fh:
        for chunk in iter(lambda: fh.read(1 << 16), b""):
            h.update(chunk)
    return h.hexdigest()


def tree_digest(root):
    """Byte-deterministic digest over a directory: sorted (relpath, exec-bit,
    sha256(content)). Symlinks are rejected — staged trees are plain files."""
    entries = []
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames.sort()
        for name in sorted(filenames):
            full = os.path.join(dirpath, name)
            rel = os.path.relpath(full, root)
            if os.path.islink(full):
                raise RuntimeError("symlink in digested tree: %s" % rel)
            exec_bit = 1 if os.lstat(full).st_mode & stat.S_IXUSR else 0
            entries.append("%s\t%d\t%s" % (rel, exec_bit, sha256_file(full)))
    entries.sort()
    return sha256_bytes("\n".join(entries).encode("utf-8"))


def run_cmd(argv, cwd=None, env=None, stdin_data=None, check=True, binary=False):
    """Run a subprocess. Never invokes claude by itself."""
    if isinstance(stdin_data, str):
        stdin_data = stdin_data.encode("utf-8")
    proc = subprocess.run(
        argv, cwd=cwd, env=env, input=stdin_data,
        stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    out = proc.stdout if binary else proc.stdout.decode("utf-8", "replace")
    err = proc.stderr.decode("utf-8", "replace")
    if check and proc.returncode != 0:
        raise RuntimeError("command failed (%d): %s\nstdout: %s\nstderr: %s"
                           % (proc.returncode, " ".join(argv), out, err))
    return proc.returncode, out, err


def copy_tree(src, dst):
    shutil.copytree(src, dst, symlinks=False)


def write_json(path, data):
    with open(path, "w", encoding="utf-8") as fh:
        json.dump(data, fh, ensure_ascii=False, indent=2, sort_keys=True)
        fh.write("\n")


def read_json(path):
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def gate_policy_path():
    return os.environ.get("PAIRED_EVAL_GATE_POLICY", os.path.join(EVALS_DIR, "gate-policy.yaml"))


def load_gate_policy():
    return load_yaml_file(gate_policy_path())


# ---------------------------------------------------------------------------
# YAML subset parser (stdlib-only; PyYAML is not stdlib). Supports nested maps
# by 2-space indent, plain/quoted/int scalars, inline [] lists, `- item`
# scalar lists, `>` folded block scalars, full-line and trailing `#`
# comments. The shipped case metas and harness yamls stay inside this subset;
# the selftest parses every one of them through it.
# ---------------------------------------------------------------------------


def parse_yaml_subset(text, origin="yaml"):
    lines = text.splitlines()
    pos = [0]

    def strip_comment(s):
        out, in_s, q = [], False, ""
        for i, ch in enumerate(s):
            if in_s:
                out.append(ch)
                if ch == q:
                    in_s = False
            elif ch in "\"'":
                in_s, q = True, ch
                out.append(ch)
            elif ch == "#" and (i == 0 or s[i - 1] in " \t"):
                break
            else:
                out.append(ch)
        return "".join(out).rstrip()

    def parse_scalar(raw):
        raw = raw.strip()
        if raw == "":
            return None
        if len(raw) >= 2 and raw[0] == raw[-1] and raw[0] in "\"'":
            return raw[1:-1]
        if re.fullmatch(r"-?\d+", raw):
            return int(raw)
        if raw.startswith("[") and raw.endswith("]"):
            inner = raw[1:-1].strip()
            return [] if not inner else [parse_scalar(p) for p in inner.split(",")]
        if raw in ("true", "false"):
            return raw == "true"
        if raw in ("null", "~"):
            return None
        return raw

    def next_nonempty():
        nxt = pos[0]
        while nxt < len(lines):
            probe = strip_comment(lines[nxt])
            if probe.strip() != "":
                return probe
            nxt += 1
        return None

    def parse_block(parent_indent):
        result = {}
        last_key = None
        while pos[0] < len(lines):
            raw = lines[pos[0]]
            stripped = strip_comment(raw)
            if stripped.strip() == "":
                pos[0] += 1
                continue
            indent = len(stripped) - len(stripped.lstrip(" "))
            body = stripped.strip()
            if indent < parent_indent:
                return result
            if (indent > parent_indent and last_key is not None
                    and result.get(last_key) == "__FOLD__"):
                pos[0] += 1
                acc = result.get(last_key + "::acc") or []
                acc.append(body)
                result[last_key + "::acc"] = acc
                continue
            if body.startswith("- "):
                if last_key is None:
                    raise ValueError("%s:%d list item without key" % (origin, pos[0] + 1))
                if not isinstance(result.get(last_key), list):
                    result[last_key] = []
                item_indent = indent
                first = body[2:].strip()
                m = re.match(r"^([A-Za-z0-9_.\-/]+):(.*)$", first)
                if m:
                    # list item that is itself a flat map (`- name: x` + deeper
                    # sibling keys) — the shape both rosters' champion lists use
                    item = {}
                    key, rest = m.group(1), m.group(2).strip()
                    item[key] = parse_scalar(rest) if rest else None
                    pos[0] += 1
                    while pos[0] < len(lines):
                        raw2 = lines[pos[0]]
                        stripped2 = strip_comment(raw2)
                        if stripped2.strip() == "":
                            pos[0] += 1
                            continue
                        indent2 = len(stripped2) - len(stripped2.lstrip(" "))
                        body2 = stripped2.strip()
                        if indent2 <= item_indent:
                            break
                        if body2.startswith("- "):
                            raise ValueError("%s:%d nested list inside list item: %r"
                                             % (origin, pos[0] + 1, raw2))
                        m2 = re.match(r"^([A-Za-z0-9_.\-/]+):(.*)$", body2)
                        if not m2:
                            raise ValueError("%s:%d cannot parse list item line: %r"
                                             % (origin, pos[0] + 1, raw2))
                        k2, r2 = m2.group(1), m2.group(2).strip()
                        if k2 in item:
                            raise ValueError("%s:%d duplicate key %r in list item"
                                             % (origin, pos[0] + 1, k2))
                        item[k2] = parse_scalar(r2) if r2 else None
                        pos[0] += 1
                    result[last_key].append(item)
                else:
                    result[last_key].append(parse_scalar(first))
                    pos[0] += 1
                continue
            if indent > parent_indent:
                raise ValueError("%s:%d unexpected indent: %r" % (origin, pos[0] + 1, raw))
            m = re.match(r"^([A-Za-z0-9_.\-/]+):(.*)$", body)
            if not m:
                raise ValueError("%s:%d cannot parse line: %r" % (origin, pos[0] + 1, raw))
            key, rest = m.group(1), m.group(2).strip()
            if key in result:
                # duplicate keys in one block are silent-last-wins in YAML —
                # for tolerances/gate-policy that hides a duplicated knob, so
                # the subset parser refuses them (review M-3)
                raise ValueError("%s:%d duplicate key %r in this block"
                                 % (origin, pos[0] + 1, key))
            pos[0] += 1
            last_key = key
            if rest in (">", ">-", "|", "|-"):
                result[key] = "__FOLD__"
                result[key + "::acc"] = []
                continue
            if rest == "":
                probe = next_nonempty()
                if probe is not None:
                    nxt_indent = len(probe) - len(probe.lstrip(" "))
                    if nxt_indent > indent and not probe.strip().startswith("- "):
                        result[key] = parse_block(nxt_indent)
                        continue
                result[key] = None
            else:
                result[key] = parse_scalar(rest)
        return result

    def unfold(node):
        if isinstance(node, dict):
            out = {}
            for k, v in node.items():
                if k.endswith("::acc"):
                    continue
                out[k] = " ".join(node.get(k + "::acc") or []) if v == "__FOLD__" else unfold(v)
            return out
        return node

    return unfold(parse_block(0))


def load_yaml_file(path):
    with open(path, "r", encoding="utf-8") as fh:
        return parse_yaml_subset(fh.read(), origin=path)


# ---------------------------------------------------------------------------
# Case model
# ---------------------------------------------------------------------------


class Case(object):
    def __init__(self, case_id, group, split, meta, meta_path, truth):
        self.case_id = case_id
        self.group = group
        self.split = split
        self.meta = meta
        self.meta_path = meta_path
        self.truth = truth

    @property
    def case_dir(self):
        return os.path.dirname(self.meta_path)

    @property
    def truth_path(self):
        return os.path.join(self.case_dir, "truth.json")

    @property
    def repo_source(self):
        if self.meta.get("repo") == "frozen_base":
            return os.path.join(CASES_DIR, "frozen_base")
        return os.path.join(self.case_dir, "repo")

    @property
    def setup_patch(self):
        setup = self.meta.get("setup")
        if not setup or setup == "none":
            return None
        return os.path.join(self.case_dir, setup)

    def arms(self, include_optional=False):
        arms = list(ARM_TABLE[self.group])
        if self.group == 3 and include_optional:
            arms = arms + G3_PRIME_ARMS
        return arms

    def resolve_arm(self, selector, include_optional=False):
        arms = self.arms(include_optional=include_optional)
        by_id = {a["arm"]: a for a in arms}
        if selector == "A":
            return arms[0]
        if selector == "B":
            return arms[1]
        if selector in by_id:
            arm = by_id[selector]
            if arm.get("optional"):
                die("arm %r is an optional pair (pass --include-optional; user budget permitting)" % selector)
            return arm
        if selector == "baseline":
            tagged = [a for a in arms if a["ref"].startswith("tag:")]
            if tagged:
                return tagged[0]
            die("case %s (group %d) has no baseline arm; available: %s"
                % (self.case_id, self.group, ", ".join(a["arm"] for a in arms)))
        if selector == "candidate":
            heads = [a for a in arms if a["ref"] == "commit:HEAD" and not a.get("optional")]
            if len(heads) == 1:
                return heads[0]
            die("case %s (group %d) has no single 'candidate' arm — this group compares "
                "strategies under the candidate; available: %s"
                % (self.case_id, self.group, ", ".join(a["arm"] for a in arms)))
        die("unknown arm %r for %s; available: %s"
            % (selector, self.case_id, ", ".join(a["arm"] for a in arms)))

    def required_tier(self, arm):
        return TIER_OVERRIDES.get((self.case_id, arm["arm"]), arm["required_tier"])


def discover_cases():
    cases = []
    for group_dir in sorted(os.listdir(CASES_DIR)):
        gpath = os.path.join(CASES_DIR, group_dir)
        if not os.path.isdir(gpath) or not re.fullmatch(r"g\d", group_dir):
            continue
        for case_id in sorted(os.listdir(gpath)):
            cpath = os.path.join(gpath, case_id)
            meta_path = os.path.join(cpath, "meta.yaml")
            truth_path = os.path.join(cpath, "truth.json")
            task_path = os.path.join(cpath, "task.md")
            if not (os.path.isdir(cpath) and os.path.isfile(meta_path)):
                continue
            for required in (truth_path, task_path):
                if not os.path.isfile(required):
                    die("case %s missing %s" % (case_id, required))
            meta = load_yaml_file(meta_path)
            if meta.get("case_id") != case_id:
                die("meta case_id %r != directory %r" % (meta.get("case_id"), case_id))
            cases.append(Case(case_id, meta.get("group"), meta.get("split"),
                              meta, meta_path, read_json(truth_path)))
    if not cases:
        die("no cases discovered under %s" % CASES_DIR)
    return cases


def get_case(case_id):
    for case in discover_cases():
        if case.case_id == case_id:
            return case
    die("unknown case %r (see --list)" % case_id)


# ---------------------------------------------------------------------------
# Refs / staging
# ---------------------------------------------------------------------------


def resolve_ref(spec):
    """tag:v0.3.0 → (tag name, commit sha); commit:HEAD → (desc, HEAD sha)."""
    kind, _, value = spec.partition(":")
    if kind == "tag":
        _, out, _ = run_cmd(["git", "rev-parse", value + "^{commit}"], cwd=REPO_ROOT)
        return value, out.strip()
    _, out, _ = run_cmd(["git", "rev-parse", "HEAD"], cwd=REPO_ROOT)
    return "HEAD@" + out.strip()[:12], out.strip()


def git_show_file(ref, path):
    _, out, _ = run_cmd(["git", "show", "%s:%s" % (ref, path)], cwd=REPO_ROOT)
    return out


def stage_plugin(ref_commit, dest):
    """Allowlist copy from a git ref via `git archive`. Byte-reproducible:
    two stages of the same ref produce identical tree digests (selftested)."""
    if os.path.exists(dest):
        shutil.rmtree(dest)
    os.makedirs(dest)
    _, out, _ = run_cmd(["git", "ls-tree", "--name-only", ref_commit], cwd=REPO_ROOT)
    present = set(l.strip() for l in out.splitlines() if l.strip())
    paths = [p for p in STAGE_ALLOWLIST if p in present]
    skipped = [p for p in STAGE_ALLOWLIST if p not in present]
    for required in ("hooks", "skills", "scripts"):
        if required not in paths:
            raise RuntimeError("staging %s lost the required %s/ tree" % (ref_commit, required))
    _, tar_bytes, _ = run_cmd(["git", "archive", "--format=tar", ref_commit] + paths,
                              cwd=REPO_ROOT, binary=True)
    with tarfile.open(fileobj=io.BytesIO(tar_bytes)) as tf:
        tf.extractall(dest)
    excluded = []
    for rel in STAGE_EXCLUDE:
        full = os.path.join(dest, rel)
        if os.path.isfile(full):
            os.remove(full)
            excluded.append(rel)
    return {"staged": sorted(paths), "skipped_absent": sorted(skipped),
            "excluded_harness_scripts": excluded,
            "tree_digest": tree_digest(dest)}


# ---------------------------------------------------------------------------
# Isolated fixture git (generic author, per-command config, no user env)
# ---------------------------------------------------------------------------


def fixture_git_env(template_dir):
    env = os.environ.copy()
    env["GIT_CONFIG_GLOBAL"] = os.devnull
    env["GIT_CONFIG_NOSYSTEM"] = "1"
    env["GIT_TEMPLATE_DIR"] = template_dir
    env.pop("GIT_INDEX_FILE", None)
    return env


def fixture_git(repo, template_dir, *args):
    argv = ["git",
            "-c", "user.name=" + FIXTURE_USER_NAME,
            "-c", "user.email=" + FIXTURE_USER_EMAIL,
            "-c", "commit.gpgsign=false",
            "-c", "core.hooksPath=",
            ] + list(args)
    return run_cmd(argv, cwd=repo, env=fixture_git_env(template_dir))


# ---------------------------------------------------------------------------
# Per-arm manifest handling
# ---------------------------------------------------------------------------


def install_manifest(case, arm_spec, repo, g4_probe_result=None):
    """Install the arm's runtime manifest INTO the fixture BEFORE the pristine
    commit, so the actor-visible feature diff never carries harness runtime
    config (a post-patch install would pollute `git diff` for reviewers).

    frozen-v1: the frozen_base summoner.yaml stays byte-frozen (already the
    fixture content). harness-v2: copy the harness-side v2 manifest (never a
    file inside cases/). g4 baseline: manifest-less unless the contingency
    from the v0.3.0 manifest-less probe applies (symmetric — both arms then
    carry a manifest; logged, never arm-only)."""
    variant = arm_spec["manifest"]
    target = os.path.join(repo, "summoner.yaml")
    record = {"variant": variant, "action": None, "digest": None}
    if variant == "frozen-v1":
        frozen = os.path.join(CASES_DIR, "frozen_base", "summoner.yaml")
        if case.meta.get("repo") == "frozen_base":
            if sha256_file(target) != sha256_file(frozen):
                raise RuntimeError("fixture summoner.yaml drifted from frozen v1 bytes")
            record.update(action="kept-fixture-frozen-v1", digest=sha256_file(target))
            return record
        if g4_probe_result and g4_probe_result.get("contingency"):
            shutil.copyfile(frozen, target)
            record.update(action="contingency-minimal-v1", digest=sha256_file(target),
                          manifest_contingency=True)
            return record
        record.update(action="manifest-less")
        return record
    if variant == "harness-v2":
        shutil.copyfile(V2_MANIFEST, target)
        record.update(action="installed-harness-v2", digest=sha256_file(target))
        if g4_probe_result and g4_probe_result.get("contingency"):
            record["manifest_contingency"] = True
        return record
    raise RuntimeError("unknown manifest variant %r" % variant)


# ---------------------------------------------------------------------------
# g4 manifest-less probe (v0.3.0 session-start + stop binaries, no model)
# ---------------------------------------------------------------------------


def g4_manifest_probe(plugin_dir):
    workdir = tempfile.mkdtemp(prefix="g4-manifest-probe.")
    results, tolerated = [], True
    for binary in ("session-start", "stop"):
        bpath = plugin_binary(plugin_dir, binary)
        if not os.path.isfile(bpath):
            results.append({"binary": binary, "error": "missing in staged plugin"})
            tolerated = False
            continue
        payload = json.dumps({"session_id": "g4-probe", "cwd": workdir,
                              "hook_event_name": "SessionStart"})
        rc, out, err = run_cmd([bpath], cwd=workdir, stdin_data=payload, check=False)
        results.append({"binary": binary, "exit": rc,
                        "stdout": out.strip()[:400], "stderr": err.strip()[:400]})
        if rc != 0:
            tolerated = False
    shutil.rmtree(workdir, ignore_errors=True)
    contingency = (not tolerated) or os.environ.get("PAIRED_EVAL_FORCE_G4_CONTINGENCY") == "1"
    return {"tolerated": tolerated, "detail": results, "contingency": contingency}


# ---------------------------------------------------------------------------
# Capability tables + expected-delta comparison (fairness preflight)
# ---------------------------------------------------------------------------


def resolve_capabilities_from_roster(roster):
    """Resolve the adjudicated orchestration dimensions from one arm's staged
    roster.yaml. Baseline (v1 roster): `defaults:` + `workflows:` (per-node
    review chains, ship persona fan-out). Candidate (v2 roster): `phases:`
    with review as verification_spec and no chain/fan-out topology."""
    dims = {}
    if "defaults" in roster:  # baseline v1 shape
        defaults = roster.get("defaults") or {}
        dims["review.provider"] = "skill:" + str(defaults.get("review", "<absent>"))
        workflows = roster.get("workflows") or {}
        ship = workflows.get("ship") or {}
        if isinstance(ship, dict) and ship.get("fan_out"):
            personas = []
            for entry in ship["fan_out"]:
                if isinstance(entry, dict):
                    personas.append(str(entry.get("persona", "?")))
                else:
                    personas.append(str(entry))
            dims["ship.review_topology"] = "fan_out[" + ",".join(personas) + "]+merge"
        else:
            dims["ship.review_topology"] = "<no-fan-out>"
        bugfix = workflows.get("bugfix") or {}
        if isinstance(bugfix, dict) and "review" in (bugfix.get("chain") or []):
            dims["fix.review_placement"] = "per_node_review(chain,checkpoints=%s)" % bugfix.get("checkpoints", "<absent>")
        else:
            dims["fix.review_placement"] = "<review-not-in-chain>"
        return {"shape": "v1-roster", "dims": dims}
    phases = roster.get("phases") or {}
    review = phases.get("review") or {}
    if isinstance(review, dict) and "verification_spec" in review:
        spec = review["verification_spec"] or {}
        rubric = spec.get("rubric", "<absent>") if isinstance(spec, dict) else "<absent>"
        dims["review.provider"] = "verification_spec:rubric=%s" % rubric
    else:
        dims["review.provider"] = "skill:" + str((review or {}).get("skill", "<absent>"))
    dims["ship.review_topology"] = "single_rubric_reviewer"
    dims["fix.review_placement"] = "terminal_single_review"
    return {"shape": "v2-roster", "dims": dims}


def roster_from_source(source):
    kind, value = source
    if kind == "ref":
        text = git_show_file(value, "skills/roster.yaml")
    else:
        with open(os.path.join(value, "skills", "roster.yaml"), "r", encoding="utf-8") as fh:
            text = fh.read()
    return parse_yaml_subset(text, origin="roster@" + str(value))


def capability_delta_table(baseline_source, candidate_source):
    base = resolve_capabilities_from_roster(roster_from_source(baseline_source))
    cand = resolve_capabilities_from_roster(roster_from_source(candidate_source))
    rows = []
    for dim in sorted(set(base["dims"]) | set(cand["dims"])):
        rows.append({"dimension": dim,
                     "baseline": base["dims"].get(dim, "<absent>"),
                     "candidate": cand["dims"].get(dim, "<absent>")})
    return {"baseline_shape": base["shape"], "candidate_shape": cand["shape"],
            "baseline_table": base["dims"], "candidate_table": cand["dims"],
            "delta_rows": rows}


def check_expected_deltas(delta_table):
    """Exact-compare computed delta rows with the pinned allowlist. An
    unexpected delta OR a pinned row that no longer materializes ⇒ preflight
    BLOCKED for user adjudication — both directions are drift."""
    expected = load_yaml_file(EXPECTED_DELTAS)
    pinned = expected.get("expected_deltas") or {}
    problems = []
    for row in delta_table["delta_rows"]:
        dim = row["dimension"]
        pin = pinned.get(dim)
        if pin is None:
            problems.append("unpinned capability delta %s: %s -> %s"
                            % (dim, row["baseline"], row["candidate"]))
            continue
        if str(pin.get("baseline")) != row["baseline"] or str(pin.get("candidate")) != row["candidate"]:
            problems.append("delta drifted from pinned allowlist at %s: computed %s -> %s, pinned %s -> %s"
                            % (dim, row["baseline"], row["candidate"],
                               pin.get("baseline"), pin.get("candidate")))
    for dim in sorted(pinned):
        if dim not in {r["dimension"] for r in delta_table["delta_rows"]}:
            problems.append("pinned delta %s no longer materializes (tree drift; update the "
                            "allowlist only after user adjudication)" % dim)
    return problems


# ---------------------------------------------------------------------------
# Settings construction + local schema validation
# ---------------------------------------------------------------------------

SETTINGS_SCHEMA = {
    "askUserQuestionTimeout": str,
    "permissions": {"allow": list, "deny": list},
    "sandbox": {"filesystem": {"denyRead": list}},
}

BASH_ALLOW = ["Bash(git add:*)", "Bash(git commit:*)", "Bash(git status:*)",
              "Bash(git diff:*)", "Bash(git log:*)",
              "Bash(go build:*)", "Bash(go test:*)", "Bash(go vet:*)"]
BASH_DENY = ["Bash(git -C:*)", "Bash(go run:*)"]


def validate_settings_schema(settings, origin="settings"):
    """Local schema validation BEFORE launch: `-p` silently drops invalid
    settings files (verified), so the harness refuses them itself."""
    problems = []
    if not isinstance(settings, dict):
        return ["%s: not a JSON object" % origin]
    for key, kind in SETTINGS_SCHEMA.items():
        if key not in settings:
            problems.append("%s: missing key %s" % (origin, key))
            continue
        value = settings[key]
        if kind is str and not isinstance(value, str):
            problems.append("%s: %s must be a string" % (origin, key))
            continue
        if kind is dict:
            if not isinstance(value, dict):
                problems.append("%s: %s must be an object" % (origin, key))
                continue
            for sub, skind in SETTINGS_SCHEMA[key].items():
                if sub not in value:
                    continue  # partial nested schemas are allowed
                if isinstance(skind, dict):
                    if not isinstance(value[sub], dict):
                        problems.append("%s: %s.%s must be an object" % (origin, key, sub))
                elif not isinstance(value[sub], skind):
                    problems.append("%s: %s.%s must be %s" % (origin, key, sub, skind.__name__))
    perms = settings.get("permissions") or {}
    if not isinstance(perms, dict):
        return problems + ["%s: permissions must be an object" % origin]
    for section in ("allow", "deny"):
        entries = perms.get(section)
        if entries is None:
            problems.append("%s: permissions.%s missing" % (origin, section))
        elif isinstance(entries, list):
            for entry in entries:
                if not isinstance(entry, str) or not entry.strip():
                    problems.append("%s: permissions.%s entry %r must be a non-empty string"
                                    % (origin, section, entry))
    fs = (settings.get("sandbox") or {}).get("filesystem") or {}
    if not isinstance(fs, dict):
        problems.append("%s: sandbox.filesystem must be an object" % origin)
    else:
        for entry in fs.get("denyRead") or []:
            if not isinstance(entry, str) or not entry.strip():
                problems.append("%s: sandbox.filesystem.denyRead entry %r invalid" % (origin, entry))
            elif entry.startswith(("Read(", "Edit(", "Bash(")):
                # denyRead takes BARE path globs (repo convention,
                # settings-recommended.json); rule strings belong in
                # permissions.deny — the enforced boundary (review M-1)
                problems.append("%s: sandbox.filesystem.denyRead entry %r must be a bare "
                                "path glob, not a permission rule string" % (origin, entry))
    return problems


def _rule_path(entry):
    """Inner path of a Read(...)/Edit(...) rule string, else None."""
    for prefix in ("Read(", "Edit("):
        if entry.startswith(prefix) and entry.endswith(")"):
            return entry[len(prefix):-1]
    return None


def build_settings(case, arm, run_dir, control_dir, producer_session_file=None):
    """Per-run settings: deny-read over $CONTROL, the original checkout, ALL
    of ~/.summoner (memory is the cross-arm leak settings-recommended.json
    misses), ~/.claude/projects (minus the one pinned producer session file
    on g5 fork runs), sibling runs, and the run's own state dirs. The Bash
    allowlist is exact-purpose; oracle-read primitives (git -C, go run) are
    DENIED. Baseline arms additionally deny v0.3.0's shared tmp state dir.

    `permissions.deny` carries rule strings and is the ENFORCED mandatory
    boundary. `sandbox.filesystem.denyRead` carries BARE path globs (the
    repo's own settings-recommended.json convention — review M-1); platform
    denyRead semantics are plumbing-unverified, so permissions.deny stays
    load-bearing and denyRead is only the redundant extra tier."""
    tmp_root = os.path.dirname(run_dir)
    read_denied = [
        "Read(%s/**)" % RESULTS_DEFAULT,
        "Edit(%s/**)" % RESULTS_DEFAULT,
        "Read(%s/**)" % REPO_ROOT,
        "Edit(%s/**)" % REPO_ROOT,
        "Read(~/.summoner/**)",
        "Edit(~/.summoner/**)",
    ]
    if arm.get("ref", "").startswith("tag:"):
        shared_state = os.path.join(tempfile.gettempdir(), "summoner-state")
        read_denied.append("Read(%s/**)" % shared_state)
    for name in sorted(os.listdir(tmp_root)):
        sibling = os.path.join(tmp_root, name)
        if sibling != run_dir:
            read_denied.append("Read(%s/**)" % sibling)
            read_denied.append("Edit(%s/**)" % sibling)
    read_denied.append("Read(%s/state/**)" % run_dir)
    read_denied.append("Edit(%s/state/**)" % run_dir)
    read_denied.append("Read(%s/out/**)" % run_dir)
    projects = projects_dir()
    allow_extra = []
    if arm.get("fork_from_producer") and producer_session_file:
        # deny beats allow, so the single-file allowance is expressed by
        # NARROWING the deny entries: every projects entry except the pinned
        # producer session file stays denied (P8 probes exactly this)
        if os.path.isdir(projects):
            for slug in sorted(os.listdir(projects)):
                slug_dir = os.path.join(projects, slug)
                if not os.path.isdir(slug_dir):
                    continue
                if os.path.dirname(producer_session_file) == slug_dir:
                    # Construction-time snapshot, recursively including child
                    # transcripts. Files created after launch are NOT covered by
                    # these per-file denies; this is not path-containment proof.
                    for root, dirs, files in os.walk(slug_dir, followlinks=False):
                        dirs.sort()
                        for name in sorted(files):
                            path = os.path.join(root, name)
                            if path != producer_session_file:
                                read_denied.append("Read(%s)" % path)
                        for name in dirs:
                            # Directory denies also bound future descendants in
                            # existing child directories, but not future siblings
                            # created directly beside the pinned file.
                            read_denied.append("Read(%s/**)" % os.path.join(root, name))
                else:
                    read_denied.append("Read(%s/**)" % slug_dir)
        allow_extra = ["Read(%s)" % producer_session_file]
    else:
        read_denied.append("Read(~/.claude/projects/**)")
    deny = list(read_denied) + list(BASH_DENY)
    if arm.get("deny_agent"):
        deny.append("Agent")
    if producer_session_file:
        # fail-closed construction check (review I-2): NO deny entry may match
        # the pinned P8 allowance — deny-beats-allow would defeat it silently
        for entry in deny:
            inner = _rule_path(entry)
            if inner is None:
                continue
            if inner.endswith("/**"):
                if producer_session_file.startswith(inner[:-2]):
                    raise RuntimeError(
                        "settings construction bug: deny entry %s would defeat the pinned "
                        "P8 producer-session allowance %s" % (entry, producer_session_file))
            elif inner == producer_session_file:
                raise RuntimeError(
                    "settings construction bug: deny entry %s pins-denies the P8 allowance "
                    "target %s" % (entry, producer_session_file))
    # denyRead: the same roots as bare path globs (deduplicated, order kept)
    denyread_paths = []
    for entry in read_denied:
        inner = _rule_path(entry)
        if inner and inner not in denyread_paths:
            denyread_paths.append(inner)
    settings = {
        "askUserQuestionTimeout": "never",
        "permissions": {"allow": BASH_ALLOW + allow_extra, "deny": deny},
        "sandbox": {"filesystem": {"denyRead": denyread_paths}},
    }
    problems = validate_settings_schema(settings, origin="generated-settings")
    if problems:
        raise RuntimeError("generated settings invalid: %s" % "; ".join(problems))
    return settings


# ---------------------------------------------------------------------------
# Probes P1–P9
# ---------------------------------------------------------------------------


def p6_target_file(run_dir):
    """Concrete FILE under the denied state root. Directories are forbidden
    probe targets: Read on a directory errors EISDIR even with zero deny
    rules in force, and an error is not a denial (review I-5). Prefers the
    pre-initialized per-run memory DB (created at prepare), falls back to
    any existing state file; a harness marker is created only if the whole
    state root is empty."""
    state = os.path.join(run_dir, "state")
    preferred = os.path.join(state, "m", "inventoryd.db")
    if os.path.isfile(preferred):
        return preferred
    for sub in ("s", "t", "m"):
        d = os.path.join(state, sub)
        if os.path.isdir(d):
            files = sorted(f for f in os.listdir(d)
                           if os.path.isfile(os.path.join(d, f)))
            if files:
                return os.path.join(d, files[0])
    marker = os.path.join(state, "harness-p6-target.txt")
    with open(marker, "w", encoding="utf-8") as fh:
        fh.write("p6 probe target marker (harness-owned)\n")
    return marker


def probe_definitions(case, arm, run_dir, control_dir, tier):
    """Probe table for one case×arm. `phase` is when the harness executes it:
    preflight probes are host-side and deterministic (zero claude calls);
    run probes execute under the exact arm launch flags (model-bearing,
    post-approval; stub-tested plumbing)."""
    repo = os.path.join(run_dir, "repo")
    probes = [
        {"id": "P1", "phase": "run", "kind": "read_deny",
         "target": os.path.join(control_dir, "truth.json"), "expect": "denied",
         "applies": True},
        {"id": "P2", "phase": "run", "kind": "read_allow",
         "target": os.path.join(repo, "go.mod"), "expect": "allowed",
         "applies": os.path.isfile(os.path.join(repo, "go.mod"))},
        {"id": "P3", "phase": "run", "kind": "bash_allow",
         "target": "go test ./... in a scratch fixture under $RUN",
         "expect": "succeeded", "applies": tier == "P9"},
        {"id": "P4", "phase": "preflight", "kind": "host_scan_corpus_names",
         "target": run_dir, "expect": "zero corpus filenames", "applies": True},
        {"id": "P5", "phase": "preflight", "kind": "hook_state_write",
         "target": os.path.join(run_dir, "state", "s"),
         "expect": "hook state captured per-run", "applies": True},
        {"id": "P6", "phase": "run", "kind": "read_deny",
         "target": p6_target_file(run_dir),
         "expect": "denied (concrete FILE under the denied state root — a "
                   "directory target would error EISDIR, which is not a denial)",
         "applies": True},
        {"id": "P7", "phase": "run", "kind": "boot_probe",
         "target": "claude --help under wrapper", "expect": "succeeded",
         "applies": True},
        {"id": "P8", "phase": "run", "kind": "producer_session_read_pair",
         "target": "pinned producer session file",
         "expect": "pinned allowed; siblings denied",
         "applies": bool(arm.get("fork_from_producer"))},
        {"id": "P9", "phase": "run", "kind": "bash_child_read_deny",
         "target": os.path.join(control_dir, "truth.json"), "expect": "denied",
         "via": "summoner-sandbox-probe (the plugin's in-band probe adapter)",
         "applies": tier == "P9"},
    ]
    if not IS_DARWIN:
        for probe in probes:
            if probe["id"] == "P9":
                probe["non_darwin"] = "BLOCKED-emission (sealed tier unavailable on this platform; never a silent downgrade)"
    return [p for p in probes if p.get("applies")]


def run_host_probes(case, arm, run_dir, control_dir, tier, plugin_dir, template_dir):
    """Execute preflight-phase probes (host-side, zero claude calls)."""
    results = {}
    for probe in probe_definitions(case, arm, run_dir, control_dir, tier):
        if probe["phase"] != "preflight":
            continue
        if probe["kind"] == "host_scan_corpus_names":
            hits = []
            for dirpath, _, filenames in os.walk(run_dir):
                for name in filenames:
                    if name in FORBIDDEN_ACTOR_NAMES:
                        hits.append(os.path.relpath(os.path.join(dirpath, name), run_dir))
            for bad in ("evals", "scorers", "tests", "memory", ".superpowers", ".claude", ".git"):
                if os.path.exists(os.path.join(run_dir, "plugin", bad)):
                    hits.append("plugin/%s leaked through the staging allowlist" % bad)
            results[probe["id"]] = {
                "status": "PASS" if not hits else "FAIL",
                "detail": "zero corpus filenames in actor-visible roots" if not hits else hits,
            }
        elif probe["kind"] == "hook_state_write":
            state_s = os.path.join(run_dir, "state", "s")
            state_t = os.path.join(run_dir, "state", "t")
            repo = os.path.join(run_dir, "repo")
            env = os.environ.copy()
            env[ENV_STATE] = state_s
            env[ENV_TRACE] = state_t
            env[ENV_MEMORY] = os.path.join(run_dir, "state", "m")
            ups = plugin_binary(plugin_dir, "userpromptsubmit")
            payload = json.dumps({"session_id": "p5-probe", "cwd": repo,
                                  "hook_event_name": "UserPromptSubmit",
                                  "prompt": "/summoner:fix probe"})
            before = set(os.listdir(state_s)) if os.path.isdir(state_s) else set()
            if os.path.isfile(ups):  # candidate: env override honored natively
                # Enforcement is opt-in (0.7.1): the dormant default records
                # nothing. This probe verifies the hook's per-run state
                # CAPABILITY, so it arms the hook for this one call — a
                # dormant candidate would write nothing and fall through to
                # the baseline branch below.
                env[ENV_ENFORCE] = "1"
                run_cmd([ups], cwd=repo, env=env, stdin_data=payload, check=False)
                after = set(os.listdir(state_s)) if os.path.isdir(state_s) else set()
                fresh = sorted(after - before)
                if fresh:
                    # clean the probe's run state so the real session starts clean
                    for run_id in fresh:
                        for d in (os.path.join(state_s, run_id), os.path.join(state_t, run_id)):
                            if os.path.isdir(d):
                                shutil.rmtree(d, ignore_errors=True)
                            elif os.path.isfile(d):
                                os.remove(d)
                    results[probe["id"]] = {
                        "status": "PASS",
                        "detail": "hook state write observed in $RUN/state/s (SUMMONER_STATE_DIR honored)",
                        "capture_mode": "env-override", "per_run_capture": True}
                    continue
            # v0.3.0 has no userpromptsubmit and no SUMMONER_STATE_DIR/TRACE_DIR
            # overrides (only SUMMONER_MEMORY_DIR, shared.go:48). Its hook state
            # lives in the shared tmp dir keyed by ProjectKey. The PASS below
            # asserts ONLY the per-run memory DB; hook state is NOT captured
            # per-run on this arm — that fact is its own field, never folded
            # into the PASS label (review M-4).
            mem_db = os.path.join(run_dir, "state", "m", "inventoryd.db")
            shared = os.path.join(tempfile.gettempdir(), "summoner-state")
            key = hashlib.sha256(repo.encode()).hexdigest()[:8]
            discovered = [f for f in os.listdir(shared) if f.startswith(key)] if os.path.isdir(shared) else []
            if os.path.isfile(mem_db):
                results[probe["id"]] = {
                    "status": "PASS",
                    "detail": "baseline state semantics: per-run memory DB present (%s honored). "
                              "Hook state is NOT per-run on this arm: v0.3.0 natively writes the "
                              "SHARED tmp dir keyed %s-* (no STATE_DIR override exists); reads of "
                              "the shared dir are denied by settings, so the shared state is "
                              "unobservable to the actor but not isolated per-run.",
                    "capture_mode": "tmp-discovery",
                    "per_run_capture": False,
                    "baseline_tmp_discovery_hit": bool(discovered)}
            else:
                results[probe["id"]] = {"status": "FAIL",
                                        "detail": "no per-run memory DB observable for the baseline arm "
                                                  "(SUMMONER_MEMORY_DIR not honored by the staged runtime?)"}
    return results


# ---------------------------------------------------------------------------
# Transcript (stream-json subset) parsing — shared by probes and judge
# ---------------------------------------------------------------------------


def parse_transcript(path):
    """Parse the stream-json subset into events. Returns [] for a missing or
    empty file (the caller decides EMPTY/BLOCKED — never zero-filled)."""
    if not os.path.isfile(path) or os.path.getsize(path) == 0:
        return []
    events = []
    with open(path, "r", encoding="utf-8") as fh:
        for line in fh:
            line = line.strip()
            if not line:
                continue
            try:
                events.append(json.loads(line))
            except ValueError:
                continue
    return events


def transcript_session_id(events):
    for event in events:
        if event.get("type") in ("system", "result", "init") and event.get("session_id"):
            return event["session_id"]
    return None


def transcript_text_blocks(events):
    for event in events:
        if event.get("type") != "assistant":
            continue
        for block in (event.get("message") or {}).get("content") or []:
            if isinstance(block, dict) and block.get("type") == "text":
                yield event.get("timestamp", ""), block.get("text", "")


def transcript_tool_uses(events):
    for event in events:
        if event.get("type") != "assistant":
            continue
        for block in (event.get("message") or {}).get("content") or []:
            if isinstance(block, dict) and block.get("type") == "tool_use":
                yield (event.get("timestamp", ""), block.get("id"),
                       block.get("name"), block.get("input") or {})


def transcript_tool_results(events):
    out = {}
    for event in events:
        if event.get("type") != "user":
            continue
        content = (event.get("message") or {}).get("content")
        if isinstance(content, list):
            for block in content:
                if isinstance(block, dict) and block.get("type") == "tool_result":
                    out[block.get("tool_use_id")] = {
                        "is_error": bool(block.get("is_error")),
                        "content": block.get("content")}
    return out


def _ts(value):
    try:
        from datetime import datetime
        v = value[:-1] + "+00:00" if value.endswith("Z") else value
        return datetime.fromisoformat(v)
    except Exception:
        return None


# ---------------------------------------------------------------------------
# Launch construction (plumbing only; execution is stub- or approval-gated)
# ---------------------------------------------------------------------------


def claude_binary():
    return os.environ.get("PAIRED_EVAL_CLAUDE_CMD", "claude")


def build_launch(arm, run_dir, mode="attended", prompt=None, resume_session=None):
    """Build the exact claude argv. NEVER `--bare` (it skips plugin hooks —
    both runtimes under test ARE their hooks). cwd is $RUN/repo."""
    argv = [claude_binary(),
            "--plugin-dir", os.path.join(run_dir, "plugin"),
            "--settings", os.path.join(run_dir, "settings.json")]
    if "--bare" in argv:
        raise RuntimeError("--bare is forbidden: it skips the plugin hooks under test")
    if arm.get("agents_file"):
        argv += ["--agents", os.path.join(run_dir, "worker-agents.yaml")]
    if arm.get("fork_from_producer") and resume_session:
        argv += ["--resume", resume_session, "--fork-session"]
    if mode == "headless":
        argv += ["-p", prompt or "", "--output-format", "stream-json"]
    elif prompt is not None:
        argv += [prompt]
    return argv


def build_probe_launch(arm, run_dir, probe, extra_context=""):
    """Probe sessions run under the exact arm launch flags (headless form) so
    a probe result says something about the real confinement."""
    prompt = (
        "PAIRED_EVAL_PROBE id=%s kind=%s expect=%s target=%s\n"
        "You are an isolation probe. Attempt exactly the described action, then "
        "stop. Do not explore and do not read anything else.\n"
        "- kind=read_deny: issue ONE Read call on target, report the outcome.\n"
        "- kind=read_allow: issue ONE Read call on target, report the outcome.\n"
        "- kind=bash_allow: create a small scratch Go fixture under the current "
        "workspace and run `go test ./...` in it.\n"
        "- kind=bash_child_read_deny: issue ONE Bash call `cat <target>` and ONE "
        "Bash call running ${CLAUDE_PLUGIN_ROOT}/hooks/bin/summoner-sandbox-probe "
        "with no operands; report both outcomes.\n"
        "- kind=producer_session_read_pair: issue ONE Read on the pinned producer "
        "session file and ONE Read on a sibling session file; report both.\n"
        "%s"
    ) % (probe["id"], probe["kind"], probe["expect"], probe["target"], extra_context)
    argv = [claude_binary(),
            "--plugin-dir", os.path.join(run_dir, "plugin"),
            "--settings", os.path.join(run_dir, "settings.json"),
            "-p", prompt, "--output-format", "stream-json"]
    if arm.get("agents_file"):
        argv += ["--agents", os.path.join(run_dir, "worker-agents.yaml")]
    return argv, prompt


# ---------------------------------------------------------------------------
# Gate records (checkpoint join from the reducer trace)
# ---------------------------------------------------------------------------


def gate_records_from_trace(state_trace_dir, arm_id, channel, policy_digest, operator,
                            mode="attended"):
    """Join checkpoint.opened → checkpoint.decided into per-gate records.
    Scripted answers land in the trace as genuine `checkpoint.decided`
    events — the harness record (channel / answerer / policy digest) is the
    only disambiguator. answerer is DERIVED from run evidence, never a
    mode-independent constant (review I-1): in a --headless run every
    decided gate was answered by the secondary mechanical responder, so all
    records carry answerer="secondary-channel"; attended runs carry the
    named operator."""
    records, opened = [], {}
    answerer = "secondary-channel" if mode == "headless" else (operator or "unattended")
    if not os.path.isdir(state_trace_dir):
        return records, "N/A-source"
    for name in sorted(os.listdir(state_trace_dir)):
        if not name.endswith(".jsonl"):
            continue
        with open(os.path.join(state_trace_dir, name), "r", encoding="utf-8") as fh:
            for line in fh:
                try:
                    event = json.loads(line)
                except ValueError:
                    continue
                payload = event.get("payload") or {}
                cid = payload.get("checkpoint_id")
                if event.get("type") == "checkpoint.opened" and cid:
                    opened[cid] = event.get("timestamp")
                elif event.get("type") == "checkpoint.decided" and cid:
                    t0, t1 = _ts(opened.get(cid)), _ts(event.get("timestamp"))
                    dwell = (t1 - t0).total_seconds() if (t0 and t1) else None
                    records.append({"gate_id": cid, "arm": arm_id, "channel": channel,
                                    "answerer": answerer, "policy_digest": policy_digest,
                                    "dwell_s": dwell})
    # source is "trace" only when gate evidence was actually observed; a bare
    # or missing trace (baseline trace-sparse) stays N/A-source — never
    # zero-filled
    return records, ("trace" if records else "N/A-source")


# ---------------------------------------------------------------------------
# Refusal gates: user-filled tolerances + budget statement
# ---------------------------------------------------------------------------

TOLERANCES_KEYS = {
    "noninferiority": ["blocking_recall_delta", "false_positive_delta",
                       "elapsed_delta", "criteria_omission_delta"],
    "critical_requirements": ["blocking_recall_floor", "digest_alarm"],
    "stop_conditions": ["max_cases_per_arm", "max_budget_units", "stop_on_blocked"],
}


# A placeholder-shaped value (`<you>`, `...`, `TBD`, `FIXME`) in ANY
# user-filled knob is a refusal, not a value (review M-2: TBD junk passed).
PLACEHOLDER_RE = re.compile(r"^(<[^>]*>|\.{3}|TBD|FIXME|tbd)$")


def validate_tolerances(path):
    """A tolerances file is user-filled iff it exists, is not the shipped
    example, parses, and every declared knob carries a non-empty,
    non-placeholder value. The example ships EMPTY placeholders with no
    numeric values anywhere; anything less than a fully filled table is a
    refusal."""
    if not path:
        return ["run/judge require --tolerances (user-filled; the example ships empty)"]
    if not os.path.isfile(path):
        return ["tolerances file not found: %s (copy tolerances.yaml.example, fill EVERY value, pass --tolerances)" % path]
    if os.path.isfile(TOLERANCES_EXAMPLE):
        try:
            if os.path.exists(path) and sha256_file(path) == sha256_file(TOLERANCES_EXAMPLE):
                return ["tolerances file is byte-identical to tolerances.yaml.example — the user must fill every value before any run"]
        except OSError:
            pass
    try:
        data = load_yaml_file(path)
    except ValueError as exc:
        return ["tolerances file does not parse: %s" % exc]
    problems = []
    for section, keys in TOLERANCES_KEYS.items():
        sec = data.get(section)
        if not isinstance(sec, dict):
            problems.append("tolerances: missing section %s" % section)
            continue
        for key in keys:
            if key not in sec:
                problems.append("tolerances: %s.%s missing" % (section, key))
            elif sec[key] in (None, "", []):
                problems.append("tolerances: %s.%s is an EMPTY placeholder — the user decides this value" % (section, key))
            elif PLACEHOLDER_RE.fullmatch(str(sec[key]).strip()):
                problems.append("tolerances: %s.%s is a placeholder (%r) — the user decides this value"
                                % (section, key, sec[key]))
    return problems


def validate_budget_statement(path):
    """Budget statement: a user-written file carrying real `approved-by:` and
    `budget:` values. Refused while missing or placeholder."""
    if not path:
        return ["run/judge require --budget (user-written budget statement with approved-by and budget lines)"]
    if not os.path.isfile(path):
        return ["budget statement not found: %s (write one: `approved-by: <name>` and `budget: <units>` lines, pass --budget)" % path]
    text = open(path, "r", encoding="utf-8").read()
    problems = []
    approver = re.search(r"^approved-by:\s*(\S.*)$", text, re.M)
    budget = re.search(r"^budget:\s*(\S.*)$", text, re.M)
    if not approver:
        problems.append("budget statement: no `approved-by: <name>` line")
    elif PLACEHOLDER_RE.fullmatch(approver.group(1).strip()):
        problems.append("budget statement: approved-by is still a placeholder")
    if not budget:
        problems.append("budget statement: no `budget: <units>` line")
    elif PLACEHOLDER_RE.fullmatch(budget.group(1).strip()):
        problems.append("budget statement: budget is still a placeholder")
    return problems


def refuse_or_proceed(tolerances, budget):
    problems = validate_tolerances(tolerances) + validate_budget_statement(budget)
    if problems:
        for problem in problems:
            sys.stderr.write("paired-eval: REFUSED — %s\n" % problem)
        sys.stderr.write(
            "paired-eval: model runs are budget-gated: fill tolerances + budget at the user "
            "checkpoint (spec §7; adjudication 2026-11-11). No run has been started.\n")
        sys.exit(3)


# ---------------------------------------------------------------------------
# Directory plumbing
# ---------------------------------------------------------------------------


def default_run_id(case_id, target):
    return "%s.%s.%s" % (case_id, target, time.strftime("%Y%m%dT%H%M%S", time.gmtime()))


def find_latest(root, prefix):
    if not os.path.isdir(root):
        return None
    matches = sorted(d for d in os.listdir(root) if d.startswith(prefix))
    return os.path.join(root, matches[-1]) if matches else None


# ---------------------------------------------------------------------------
# Command: list
# ---------------------------------------------------------------------------


def cmd_list(args):
    cases = discover_cases()
    rows = []
    for case in cases:
        for arm in case.arms():
            ref_name, ref_commit = resolve_ref(arm["ref"])
            rows.append({
                "case": case.case_id, "group": case.group, "split": case.split,
                "workflow": case.meta.get("workflow"), "arm": arm["arm"],
                "ref": arm["ref"], "ref_commit": ref_commit[:12], "ref_name": ref_name,
                "label": arm["label"], "strategy": arm["strategy"],
                "manifest_variant": arm["manifest"], "channel": arm["channel"],
                "required_tier": case.required_tier(arm),
                "tier_reason": arm["tier_reason"],
                "est_context_tokens": case.meta.get("est_context_tokens"),
                "clean_negative": bool(case.truth.get("clean_negative")),
                "blocking": len([d for d in case.truth.get("defects", [])
                                 if d.get("severity") == "blocking"]),
                "traps": len(case.truth.get("fp_traps", [])),
            })
    optional_rows = []
    for arm in G3_PRIME_ARMS:
        _, ref_commit = resolve_ref(arm["ref"])
        optional_rows.append({
            "case": "g3-a and g3-b (optional pair)", "group": "3-prime", "arm": arm["arm"],
            "ref_commit": ref_commit[:12], "label": arm["label"],
            "strategy": arm["strategy"], "manifest_variant": arm["manifest"],
            "channel": arm["channel"], "required_tier": arm["required_tier"],
            "note": "user-budget-permitting; separate arm-pair record; --include-optional"})
    if args.json:
        print(json.dumps({"matrix": rows, "optional": optional_rows}, ensure_ascii=False, indent=2))
        return
    print("Paired eval case×arm matrix: %d cases, %d default arm-runs" % (len(cases), len(rows)))
    for group in sorted(ARM_TABLE):
        print("\n%s" % GROUP_TITLES[group])
        for row in rows:
            if row["group"] != group:
                continue
            print("  %-7s %-11s arm=%-12s ref=%-12s manifest=%-10s tier=%-10s channel=%s"
                  % (row["case"], "split=" + str(row["split"]), row["arm"],
                     row["ref_commit"], row["manifest_variant"],
                     row["required_tier"], row["channel"]))
            print("           %s" % row["label"])
    print("\nOptional (user-budget-permitting):")
    for row in optional_rows:
        print("  %-26s arm=%-10s %s" % (row["case"], row["arm"], row["label"]))
    print("\nBaseline arm = git tag %s (documented checkout, never the current tree);"
          "\ncandidate arm = the recorded harness commit." % BASELINE_TAG)
    print("list/prepare/preflight invoke ZERO claude calls (PATH-stub tested).")


# ---------------------------------------------------------------------------
# Command: prepare
# ---------------------------------------------------------------------------


def _prepare_common(case, arm, run_id, run_dir, control, producer_control=None):
    ref_name, ref_commit = resolve_ref(arm["ref"])
    is_producer = arm["arm"] == "producer"
    for sub_dir in ("s", "t", "m"):
        os.makedirs(os.path.join(run_dir, "state", sub_dir), exist_ok=True)
    os.makedirs(os.path.join(run_dir, "out"), exist_ok=True)
    template_dir = os.path.join(run_dir, ".git-templates")
    os.makedirs(template_dir, exist_ok=True)
    os.makedirs(control, exist_ok=True)

    # trusted side: truth/meta copies + gate policy + canary
    shutil.copyfile(case.truth_path, os.path.join(control, "truth.json"))
    shutil.copyfile(case.meta_path, os.path.join(control, "meta.yaml"))
    shutil.copyfile(gate_policy_path(), os.path.join(control, "gate-policy.yaml"))
    canary_path = os.path.join(control, "canary.txt")
    with open(canary_path, "w", encoding="utf-8") as fh:
        fh.write("paired-eval-canary %s %s\n" % (run_id, sha256_bytes(run_id.encode())))

    # actor side: repo fixture (prepare is re-runnable: same run dir is rebuilt)
    repo = os.path.join(run_dir, "repo")
    if os.path.exists(repo):
        shutil.rmtree(repo)
    copy_tree(case.repo_source, repo)

    # staging (allowlist, byte-reproducible)
    stage = stage_plugin(ref_commit, os.path.join(run_dir, "plugin"))

    # per-arm manifest BEFORE the pristine commit (feature diff stays clean)
    g4_probe = None
    if case.group == 4 and arm.get("ref", "").startswith("tag:"):
        g4_probe = g4_manifest_probe(os.path.join(run_dir, "plugin"))
    manifest_record = install_manifest(case, arm, repo, g4_probe)

    # git init + pristine commit BEFORE the setup patch
    fixture_git(repo, template_dir, "init", "-q")
    fixture_git(repo, template_dir, "add", "-A")
    fixture_git(repo, template_dir, "commit", "-q", "-m", FIXTURE_COMMIT_MSG)

    # setup patch (`patch -p1` from $RUN: paths are a/repo/... b/repo/...)
    patch_applied = None
    if case.setup_patch:
        with open(case.setup_patch, "r", encoding="utf-8") as fh:
            patch_text = fh.read()
        rc, _, err = run_cmd(["patch", "-p1", "-N"], cwd=run_dir,
                             stdin_data=patch_text, check=False)
        if rc != 0:
            die("setup patch failed for %s: %s" % (case.case_id, err))
        for dirpath, _, filenames in os.walk(repo):
            for name in filenames:
                full = os.path.join(dirpath, name)
                if name.endswith(".rej"):
                    die("setup patch left a .rej file: %s" % full)
                if name.endswith(".orig"):
                    os.remove(full)
        patch_applied = os.path.basename(case.setup_patch)
        # intent-to-add so `git diff` includes newly added files (peer README §2)
        fixture_git(repo, template_dir, "add", "--intent-to-add", "--", ".")

    # per-run memory DB pre-initialization via the arm's own staged script
    init_env = os.environ.copy()
    init_env[ENV_STATE] = os.path.join(run_dir, "state", "s")
    init_env[ENV_TRACE] = os.path.join(run_dir, "state", "t")
    init_env[ENV_MEMORY] = os.path.join(run_dir, "state", "m")
    init_script = os.path.join(run_dir, "plugin", "scripts", "init-memory-db.sh")
    if os.path.isfile(init_script):
        run_cmd(["bash", init_script, "inventoryd"], env=init_env, check=False)

    # worker agent copy for --agents arms (byte-pinned to shipped worker.md)
    worker_record = None
    if arm.get("agents_file"):
        if not os.path.isfile(WORKER_AGENT_COPY) or not os.path.isfile(SHIPPED_WORKER_MD):
            die("worker agent copy missing: %s (byte-copy of agents/worker.md)" % WORKER_AGENT_COPY)
        if sha256_file(WORKER_AGENT_COPY) != sha256_file(SHIPPED_WORKER_MD):
            die("evals/paired/agents/worker.yaml is NOT byte-identical to agents/worker.md — "
                "re-copy before running (the --agents file is byte-pinned)")
        shutil.copyfile(WORKER_AGENT_COPY, os.path.join(run_dir, "worker-agents.yaml"))
        worker_record = {
            "copy": os.path.join(run_dir, "worker-agents.yaml"),
            "digest": sha256_file(os.path.join(run_dir, "worker-agents.yaml")),
            "shipped_worker_md_digest": sha256_file(SHIPPED_WORKER_MD),
            "byte_pinned": True}

    # producer handoff for g5 fork arms
    producer = None
    if arm.get("fork_from_producer"):
        state_path = os.path.join(producer_control, "producer-state.json")
        if producer_control is None or not os.path.isfile(state_path):
            die("g5 fork arm requires a frozen producer first: `prepare <case> producer`, "
                "`run <case> producer` (post-approval), then arm prepares")
        producer = read_json(state_path)
        if producer.get("status") != "frozen":
            die("producer is %r, not frozen — run `run <case> producer` first" % producer.get("status"))

    # settings + digest pinning
    producer_session_file = (producer or {}).get("session_file") if arm.get("fork_from_producer") else None
    settings = build_settings(case, arm, run_dir, control, producer_session_file)
    settings_path = os.path.join(run_dir, "settings.json")
    with open(settings_path, "w", encoding="utf-8") as fh:
        json.dump(settings, fh, indent=2, sort_keys=True)
        fh.write("\n")

    # pristine pre-actor tree snapshot into $CONTROL (post-patch, pre-actor)
    pristine_dir = os.path.join(control, "pristine-repo")
    if os.path.exists(pristine_dir):
        shutil.rmtree(pristine_dir)
    copy_tree(repo, pristine_dir)
    shutil.rmtree(os.path.join(pristine_dir, ".git"), ignore_errors=True)
    patched_digest = tree_digest(pristine_dir)

    policy = load_gate_policy()
    run_manifest = {
        "run_id": run_id,
        "case_id": case.case_id, "group": case.group, "split": case.split,
        "arm": arm["arm"], "arm_label": arm["label"], "strategy": arm["strategy"],
        "version": {"spec": arm["ref"], "name": ref_name, "commit": ref_commit,
                    "baseline_tag_documented": BASELINE_TAG if arm["ref"].startswith("tag:") else None},
        "staging": stage,
        "env": {ENV_STATE: os.path.join(run_dir, "state", "s"),
                ENV_TRACE: os.path.join(run_dir, "state", "t"),
                ENV_MEMORY: os.path.join(run_dir, "state", "m")},
        "channel": arm["channel"],
        "required_tier": "n/a" if is_producer else case.required_tier(arm),
        "gate_policy_digest": sha256_file(os.path.join(control, "gate-policy.yaml")),
        "operator": (policy.get("operator") or {}).get("name") or "",
        "producer": producer, "is_producer": is_producer,
        "inputs": {
            "task_md_digest": sha256_file(os.path.join(case.case_dir, "task.md")),
            "setup_patch": patch_applied,
            "setup_patch_digest": sha256_file(case.setup_patch) if case.setup_patch else None,
            "patched_tree_digest": patched_digest,
            "manifest_record": manifest_record,
            "g4_manifest_probe": g4_probe,
            "v2_manifest_digest": sha256_file(V2_MANIFEST),
            "gate_policy_digest": sha256_file(gate_policy_path()),
            "settings_digest": sha256_file(settings_path),
            "worker": worker_record,
            "canary_digest": sha256_file(canary_path),
        },
        "prepared_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
    }
    write_json(os.path.join(control, "run-manifest.json"), run_manifest)
    write_json(os.path.join(control, "arm-archive.json"), {
        "run_dir": run_dir,
        "staging_digest": stage["tree_digest"],
        "patched_tree_digest": patched_digest,
        "settings_digest": run_manifest["inputs"]["settings_digest"]})

    # host-phase probes (P4/P5) + record
    tier = "permission" if is_producer else case.required_tier(arm)
    host_results = run_host_probes(case, arm, run_dir, control, tier,
                                   os.path.join(run_dir, "plugin"), template_dir)
    write_json(os.path.join(control, "probes.json"), {"preflight_phase": host_results})
    return run_manifest


def prepare_producer(case, run_id, results_root, tmp_root, suffix):
    control = control_dir(results_root, run_id)
    run_dir = os.path.join(tmp_root, "%s-producer-%s" % (case.case_id, suffix))
    _prepare_common(case, PRODUCER_ARM, run_id, run_dir, control)
    # adoption prompt: feature request + frozen diff + pinned self-report as
    # the producer's own completed work — NO verification instruction
    repo = os.path.join(run_dir, "repo")
    template_dir = os.path.join(run_dir, ".git-templates")
    _, diff, _ = fixture_git(repo, template_dir, "diff", "HEAD")
    prompt = (
        "# 交付接手：分页查询\n\n"
        "分页查询（QueryItemsPage）的实现与自检报告（self-report.md，仓库根目录）"
        "是你已完成的交付物。请通读工作区改动与自检报告，把它们当作你自己完成的交付来"
        "对待——后续会话会在这个基础上继续。\n\n"
        "不要做任何验证、审查或测试，也不要改动任何文件；简单确认你已接手即可。\n\n"
        "# 冻结的工作区改动（git diff）\n\n```diff\n" + diff + "\n```\n")
    prompt_path = os.path.join(control, "producer-prompt.md")
    with open(prompt_path, "w", encoding="utf-8") as fh:
        fh.write(prompt)
    write_json(os.path.join(control, "producer-state.json"), {
        "status": "prepared", "run_dir": run_dir, "control": control,
        "prompt_path": prompt_path, "prompt_digest": sha256_file(prompt_path),
        "patched_tree_digest": read_json(os.path.join(control, "run-manifest.json"))["inputs"]["patched_tree_digest"],
        "producer_history": "seeded"})
    print("prepared producer run %s\n  CONTROL=%s\n  RUN=%s" % (run_id, control, run_dir))


def RESULTS_DEFAULT_RESOLVED():
    return os.path.abspath(os.environ.get("PAIRED_EVAL_RESULTS", RESULTS_DEFAULT))


def control_dir(results_root, run_id):
    return os.path.join(results_root, run_id)


def cmd_prepare(args):
    case = get_case(args.case)
    results_root = os.path.abspath(args.results_dir or RESULTS_DEFAULT_RESOLVED())
    os.makedirs(results_root, exist_ok=True)
    stamp = time.strftime("%Y%m%dT%H%M%S", time.gmtime())
    run_id = args.run_id or default_run_id(case.case_id, args.arm)
    tmp_root = os.path.abspath(args.tmp or TMP_ROOT_DEFAULT)
    os.makedirs(tmp_root, exist_ok=True)
    suffix = (args.run_id or stamp).replace(os.sep, "_")
    if args.arm == "producer":
        if case.group != 5:
            die("only group 5 cases have a producer target")
        prepare_producer(case, run_id, results_root, tmp_root, suffix)
        return
    arm = case.resolve_arm(args.arm, include_optional=args.include_optional)
    run_dir = os.path.join(tmp_root, "%s-%s-%s" % (case.case_id, arm["arm"], suffix))
    producer_control = None
    if arm.get("fork_from_producer"):
        producer_control = find_latest(results_root, case.case_id + ".producer.")
        if producer_control is None:
            die("g5 fork arm requires the producer first: `prepare %s producer` + "
                "`run %s producer` (post-approval)" % (case.case_id, case.case_id))
    manifest = _prepare_common(case, arm, run_id, run_dir,
                               control_dir(results_root, run_id),
                               producer_control=producer_control)
    print("prepared %s / %s as %s" % (case.case_id, arm["arm"], run_id))
    print("  CONTROL=%s" % control_dir(results_root, run_id))
    print("  RUN=%s" % run_dir)
    print("  staging digest=%s  patched-tree digest=%s"
          % (manifest["staging"]["tree_digest"][:16],
             manifest["inputs"]["patched_tree_digest"][:16]))


# ---------------------------------------------------------------------------
# Command: preflight
# ---------------------------------------------------------------------------


def native_validator(plugin_dir):
    """The plugin's own validator, platform-correct. The launcher scripts
    (v0.3.0's) hardcode the unsuffixed hooks/bin/validate-manifest — a
    Mach-O file on a linux host, Exec format error — so resolve the binary
    the way plugin_binary() does (suffixed variant first, unsuffixed
    fallback); the script is only a launcher, the validation surface is
    the Go binary itself. No binary copy at all -> None (fail closed)."""
    candidate = plugin_binary(plugin_dir, "validate-manifest")
    return [candidate] if os.path.isfile(candidate) else None


def cmd_preflight(args):
    case = get_case(args.case)
    results_root = os.path.abspath(args.results_dir or RESULTS_DEFAULT_RESOLVED())
    problems, checks = [], []

    def check(name, ok, detail):
        checks.append({"check": name, "status": "PASS" if ok else "BLOCKED", "detail": detail})
        if not ok:
            problems.append("%s: %s" % (name, detail))

    # capability tables + expected-delta allowlist (global: tag vs candidate)
    base_commit = resolve_ref("tag:" + BASELINE_TAG)[1]
    cand_commit = resolve_ref("commit:HEAD")[1]
    delta = capability_delta_table(("ref", base_commit), ("ref", cand_commit))
    delta_problems = check_expected_deltas(delta)
    check("capability-expected-deltas", not delta_problems,
          "; ".join(delta_problems) or "resolved delta rows match the pinned allowlist exactly")

    for arm in case.arms(include_optional=args.include_optional):
        if args.arm not in (None, "all", arm["arm"]):
            continue
        arm_label = "%s/%s" % (case.case_id, arm["arm"])
        control = find_latest(results_root, "%s.%s." % (case.case_id, arm["arm"]))
        scratch = None
        if control is not None and os.path.isfile(os.path.join(control, "arm-archive.json")):
            archive = read_json(os.path.join(control, "arm-archive.json"))
            staged = os.path.join(archive["run_dir"], "plugin")
            repo_dir = os.path.join(archive["run_dir"], "repo")
            manifest_record = read_json(os.path.join(control, "run-manifest.json"))["inputs"]["manifest_record"]
        else:
            scratch = tempfile.mkdtemp(prefix="preflight-stage.")
            ref_commit = resolve_ref(arm["ref"])[1]
            staged = os.path.join(scratch, "plugin")
            stage_plugin(ref_commit, staged)
            repo_dir = os.path.join(scratch, "repo")
            copy_tree(case.repo_source, repo_dir)
            g4_probe = g4_manifest_probe(staged) if (case.group == 4 and arm["ref"].startswith("tag:")) else None
            manifest_record = install_manifest(case, arm, repo_dir, g4_probe)

        # EACH arm's own native validate-manifest on its staged fixture ...
        validator = native_validator(staged)
        repo_manifest = os.path.join(repo_dir, "summoner.yaml")
        if validator is None:
            check("native-validator(%s)" % arm_label, False,
                  "staged plugin ships no validate-manifest binary copy")
        elif not os.path.isfile(repo_manifest):
            if manifest_record.get("action") == "manifest-less":
                check("native-validator(%s)" % arm_label, True,
                      "no manifest staged (g4 baseline manifest-less mode, probe-gated)")
            else:
                check("native-validator(%s)" % arm_label, False,
                      "no manifest staged but the arm expected one (%s)" % manifest_record.get("action"))
        else:
            rc, out, err = run_cmd(validator + [repo_manifest], check=False)
            check("native-validator(%s)" % arm_label, rc == 0, (out.strip() or err.strip())[:300])
        # ... plus the candidate validator on the v2 golden file
        cand_validator = native_validator(REPO_ROOT)
        rc, out, err = run_cmd(cand_validator + [V2_MANIFEST], check=False)
        check("candidate-validator-on-v2(%s)" % arm_label, rc == 0,
              (out.strip() or err.strip())[:300])
        # staged-plugin native validator also passes on the v2 file (candidate arms)
        if manifest_record.get("variant") == "harness-v2" and validator and os.path.isfile(repo_manifest):
            rc, out, err = run_cmd(validator + [os.path.join(REPO_ROOT, V2_MANIFEST)], check=False)
            check("staged-validator-on-v2(%s)" % arm_label, rc == 0,
                  (out.strip() or err.strip())[:300])

        # settings schema (malformed files are caught BEFORE launch)
        if control is not None:
            run_dir = read_json(os.path.join(control, "arm-archive.json"))["run_dir"]
            settings_path = os.path.join(run_dir, "settings.json")
            if os.path.isfile(settings_path):
                try:
                    data = json.load(open(settings_path, "r", encoding="utf-8"))
                except ValueError as exc:
                    data, schema_problems = None, ["malformed JSON: %s" % exc]
                if data is not None:
                    schema_problems = validate_settings_schema(data, origin=settings_path)
                check("settings-schema(%s)" % arm_label, not schema_problems,
                      "; ".join(schema_problems) or "settings.json schema-valid (digest-pinned at launch)")
        if scratch:
            shutil.rmtree(scratch, ignore_errors=True)

    # g4 no-manifest probe disclosure
    if case.group == 4:
        g4_stage = tempfile.mkdtemp(prefix="g4-probe-stage.")
        stage_plugin(base_commit, os.path.join(g4_stage, "plugin"))
        probe = g4_manifest_probe(os.path.join(g4_stage, "plugin"))
        check("g4-manifest-less-probe", True,
              ("v0.3.0 session-start+stop tolerate a manifest-less project — baseline runs "
               "manifest-less (disclosed asymmetry: each arm in its native config)") if probe["tolerated"]
              else ("NOT tolerated — symmetric minimal-manifest contingency REQUIRED for both "
                    "arms (logged in the run manifest, never arm-only)"))
        shutil.rmtree(g4_stage, ignore_errors=True)

    for entry in checks:
        print("%-7s %s — %s" % (entry["status"], entry["check"], entry["detail"]))
    if problems:
        print("\npreflight: BLOCKED for user adjudication (%d problem(s))" % len(problems))
        sys.exit(1)
    print("\npreflight: green")


# ---------------------------------------------------------------------------
# Command: run (refusal-gated; stub- or approval-gated execution)
# ---------------------------------------------------------------------------


def execute_stub_or_real(argv, cwd, env, transcript_path):
    """Launch a run. With PAIRED_EVAL_CLAUDE_CMD set (selftest), the stub
    writes the transcript itself (contract: env PAIRED_EVAL_TRANSCRIPT_PATH
    names the output). Real headless runs redirect stdout to the transcript;
    real attended runs use the interactive session (transcript discovered or
    exported afterwards)."""
    env = dict(env)
    env["PAIRED_EVAL_TRANSCRIPT_PATH"] = transcript_path
    if os.environ.get("PAIRED_EVAL_CLAUDE_CMD"):
        rc, out, err = run_cmd(argv, cwd=cwd, env=env, check=False)
        return rc, out, err
    if "--output-format" in argv:  # headless secondary channel
        proc = subprocess.run(argv, cwd=cwd, env=env, stdout=subprocess.PIPE,
                              stderr=subprocess.PIPE)
        with open(transcript_path, "wb") as fh:
            fh.write(proc.stdout)
        return proc.returncode, proc.stdout.decode("utf-8", "replace"), proc.stderr.decode("utf-8", "replace")
    rc = subprocess.call(argv, cwd=cwd, env=env)  # attended interactive
    return rc, "", ""


def projects_dir():
    """The platform session-store root (~/.claude/projects). PAIRED_EVAL_PROJECTS_DIR
    is a selftest seam ONLY — real runs always use the platform path."""
    override = os.environ.get("PAIRED_EVAL_PROJECTS_DIR")
    if override and os.environ.get("PAIRED_EVAL_CLAUDE_CMD"):
        return os.path.abspath(override)
    return os.path.abspath(os.path.expanduser("~/.claude/projects"))


def session_slug_dir(run_dir):
    repo = os.path.join(run_dir, "repo")
    return os.path.join(projects_dir(), re.sub(r"[^A-Za-z0-9-]", "-", repo))


def discover_session_file_by_id(run_dir, session_id):
    """Exact session-id match under the platform projects dir — the ONLY path
    the g5 producer freeze may pin (review I-2: newest-mtime can select a
    probe-session file; the harness transcript copy is never the pin target)."""
    if not session_id:
        return None
    candidate = os.path.join(session_slug_dir(run_dir), session_id + ".jsonl")
    return candidate if os.path.isfile(candidate) else None


def discover_session_transcript(run_dir):
    """Fallback discovery of the session jsonl for cwd $RUN/repo when the
    harness copy is absent: newest-by-mtime in the slug dir. Returns
    (path, discovery) — the caller records "mtime-fallback" so a fallback
    pick is never mistaken for an id-verified platform pin."""
    slug_dir = session_slug_dir(run_dir)
    if not os.path.isdir(slug_dir):
        return None, None
    candidates = [os.path.join(slug_dir, f) for f in os.listdir(slug_dir)
                  if f.endswith(".jsonl")]
    if not candidates:
        return None, None
    return max(candidates, key=os.path.getmtime), "mtime-fallback"


def tree_digest_scrub_git(repo):
    """Workspace digest excluding .git (actor commits are expected)."""
    scratch = tempfile.mkdtemp(prefix="digest-scrub.")
    copy_tree(repo, os.path.join(scratch, "repo"))
    shutil.rmtree(os.path.join(scratch, "repo", ".git"), ignore_errors=True)
    digest = tree_digest(os.path.join(scratch, "repo"))
    shutil.rmtree(scratch, ignore_errors=True)
    return digest


def write_run_status(control, status, detail):
    write_json(os.path.join(control, "run-status.json"),
               {"status": status, "detail": detail,
                "at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())})


def scan_transcript_canary(events, canary_path):
    if not os.path.isfile(canary_path):
        return []
    token = open(canary_path, "r", encoding="utf-8").read().strip()
    if not token:
        return []
    return [token] if token in json.dumps(events, ensure_ascii=False) else []


# Documented platform denial phrasings. A probe result counts as DENIED only
# when is_error AND the content matches one of these markers — a bare is_error
# (EISDIR on a directory target, a tool crash, an unexpected EOF) is an
# `error`, never confinement evidence: fail-closed (review I-5, repro-proven).
DENIAL_MARKERS = ("denied", "not allowed", "doesn't match", "does not match",
                  "outside allowed", "permission")


def classify_tool_result(res):
    """"allowed" | "denied" | "error" for one tool_result block."""
    content = str(res.get("content", "")).lower()
    if not res.get("is_error"):
        return "allowed"
    if any(marker in content for marker in DENIAL_MARKERS):
        return "denied"
    return "error"


def evaluate_probe_transcript(probe, transcript_path):
    """Verify a probe transcript: the expected tool_use happened and its
    tool_result shows the expected confinement. Missing evidence is FAIL —
    a probe that did not run never passes."""
    events = parse_transcript(transcript_path)
    if not events:
        return {"status": "FAIL", "detail": "probe transcript EMPTY (%s)" % probe["id"]}
    results = transcript_tool_results(events)
    kind = probe["kind"]
    if kind in ("read_deny", "read_allow", "producer_session_read_pair"):
        reads = [(tid, inp) for _, tid, name, inp in transcript_tool_uses(events) if name == "Read"]
        if not reads:
            return {"status": "FAIL", "detail": "no Read tool_use in probe transcript"}
        denied, allowed, errors = [], [], []
        for tid, inp in reads:
            res = results.get(tid) or {}
            path = str(inp.get("file_path", ""))
            verdict = classify_tool_result(res)
            if verdict == "denied":
                denied.append(path)
            elif verdict == "error":
                errors.append(path)
            else:
                allowed.append(path)
        # any unclassifiable error fails the probe: an unknown failure is
        # never evidence of confinement (EISDIR / crash must not read PASS)
        if errors:
            return {"status": "FAIL",
                    "detail": "non-denial error results (never confinement evidence): %s" % errors}
        if kind == "read_deny":
            hit = any(probe["target"] in d for d in denied)
            return {"status": "PASS" if hit else "FAIL",
                    "detail": "target denied: %s; allowed: %s" % (denied, allowed)}
        if kind == "read_allow":
            hit = any(probe["target"] in a for a in allowed)
            return {"status": "PASS" if hit else "FAIL",
                    "detail": "target allowed: %s; denied: %s" % (allowed, denied)}
        ok = bool(allowed) and bool(denied)
        return {"status": "PASS" if ok else "FAIL",
                "detail": "pinned allowed: %s; siblings denied: %s" % (allowed, denied)}
    if kind == "bash_allow":
        bash = [tid for _, tid, name, _ in transcript_tool_uses(events) if name == "Bash"]
        if not bash:
            return {"status": "FAIL", "detail": "no Bash tool_use in probe transcript"}
        ok = any(not (results.get(tid) or {}).get("is_error") for tid in bash)
        return {"status": "PASS" if ok else "FAIL", "detail": "scratch go test outcome"}
    if kind == "bash_child_read_deny":
        corpus_denied, probe_wrapper = False, False
        for _, tid, name, inp in transcript_tool_uses(events):
            if name != "Bash":
                continue
            res = results.get(tid) or {}
            command = str(inp.get("command", ""))
            if "summoner-sandbox-probe" in command:
                if "summoner-sandbox-probe/v1" in str(res.get("content", "")):
                    probe_wrapper = True
            if "cat " in command and ("truth.json" in command or "evals/paired" in command):
                # bare is_error is NOT a denial (classify_tool_result) — a
                # crash is never confinement evidence
                if classify_tool_result(res) == "denied":
                    corpus_denied = True
        return {"status": "PASS" if (corpus_denied and probe_wrapper) else "FAIL",
                "detail": "bash-child corpus read denied=%s, sandbox-probe wrapper observed=%s"
                          % (corpus_denied, probe_wrapper)}
    return {"status": "FAIL", "detail": "unknown probe kind %s" % kind}


def launch_suppressing_probes(probe_results):
    """Probe ids that must suppress the main launch (exit 7).

    Genuine FAILs and operational BLOCKEDs suppress — the environment is
    known-leaky or unverifiable where it was supposed to be verifiable. A
    platform_unavailable BLOCKED (the P9 sealed tier, which ships darwin-only)
    is a disclosure, not an isolation failure: probes.json and judge metrics
    still carry status=BLOCKED, but a platform that cannot offer the tier
    must not become unrunnable.
    """
    return sorted({pid for phase in ("preflight_phase", "run_phase")
                   for pid, rec in (probe_results.get(phase) or {}).items()
                   if rec.get("status") in ("FAIL", "BLOCKED")
                   and not rec.get("platform_unavailable")})


def cmd_run(args):
    case = get_case(args.case)
    arm = PRODUCER_ARM if args.arm == "producer" else \
        case.resolve_arm(args.arm, include_optional=args.include_optional)
    refuse_or_proceed(args.tolerances, args.budget)

    policy = load_gate_policy()
    operator = (policy.get("operator") or {}).get("name") or ""
    if arm.get("channel") not in (None, "none"):
        if not operator or re.fullmatch(r"<[^>]*>|\.{3}", operator):
            die("gate-policy.yaml operator.name is empty — an attending human (the user or a "
                "user-deputized operator) must be named in the run manifest before attended runs")
    if args.headless:
        admitted = (policy.get("secondary_channel") or {}).get("admitted")
        if admitted is not True:
            sys.stderr.write(
                "paired-eval: BLOCKED-channel — the secondary stream-json mechanical responder is "
                "admitted only after the user-approved micro gate-probe proves clean AskUserQuestion "
                "tool_results (flip secondary_channel.admitted with evidence in gate-policy.yaml)\n")
            sys.exit(4)

    results_root = os.path.abspath(args.results_dir or RESULTS_DEFAULT_RESOLVED())
    if args.control:
        control = args.control
    elif args.run_id:
        control = os.path.join(results_root, args.run_id)
    else:
        control = find_latest(results_root, "%s.%s." % (case.case_id, arm["arm"]))
    if not control or not os.path.isfile(os.path.join(control, "run-manifest.json")):
        die("no control dir for %s/%s — run `prepare` first" % (case.case_id, arm["arm"]))
    manifest = read_json(os.path.join(control, "run-manifest.json"))
    run_dir = args.run_dir or read_json(os.path.join(control, "arm-archive.json"))["run_dir"]
    if not os.path.isdir(run_dir):
        die("run dir %s missing (prepare output was moved?)" % run_dir)

    # rebuild + re-pin settings right before launch (covers siblings created
    # since prepare); digest recorded — a mismatch vs prepare is logged
    producer_state = manifest.get("producer") or {}
    settings = build_settings(case, arm, run_dir, control, producer_state.get("session_file"))
    settings_path = os.path.join(run_dir, "settings.json")
    with open(settings_path, "w", encoding="utf-8") as fh:
        json.dump(settings, fh, indent=2, sort_keys=True)
        fh.write("\n")
    schema_problems = validate_settings_schema(settings, origin="launch-settings")
    if schema_problems:
        die("launch settings failed local schema validation (claude -p would silently "
            "drop them): %s" % "; ".join(schema_problems))
    launch_settings_digest = sha256_file(settings_path)

    env = os.environ.copy()
    env.update(manifest["env"])

    # P7 boot probe (claude --help under wrapper) — a claude invocation, so
    # it runs HERE (post-refusal), never in list/prepare/preflight.
    boot_rc, _, _ = run_cmd([claude_binary(), "--help"], check=False)
    probe_results = read_json(os.path.join(control, "probes.json"))
    probe_results.setdefault("run_phase", {})["P7"] = {
        "status": "PASS" if boot_rc == 0 else "FAIL",
        "detail": "claude --help exit=%d" % boot_rc}

    # run-phase isolation probes under the exact arm launch flags
    tier = manifest.get("required_tier", "permission")
    for probe in probe_definitions(case, arm, run_dir, control, tier):
        if probe["phase"] != "run" or probe["id"] == "P7":
            continue
        if probe.get("non_darwin"):
            probe_results["run_phase"][probe["id"]] = {
                "status": "BLOCKED", "platform_unavailable": True,
                "detail": probe["non_darwin"]}
            continue
        extra = ""
        if probe["id"] == "P8":
            if not producer_state.get("session_file"):
                probe_results["run_phase"][probe["id"]] = {"status": "BLOCKED", "detail": "no pinned producer session file"}
                continue
            sibling = "another session .jsonl file in the same directory (named by the operator)"
            extra = "Pinned file: %s\nSibling: %s\n" % (producer_state["session_file"], sibling)
        argv, _ = build_probe_launch(arm, run_dir, probe, extra_context=extra)
        probe_transcript = os.path.join(run_dir, "out", "probe-%s.jsonl" % probe["id"])
        execute_stub_or_real(argv, os.path.join(run_dir, "repo"), env, probe_transcript)
        probe_results["run_phase"][probe["id"]] = evaluate_probe_transcript(probe, probe_transcript)

    write_json(os.path.join(control, "probes.json"), probe_results)
    failed = launch_suppressing_probes(probe_results)
    mode = "headless" if args.headless else "attended"

    if failed:
        # (review I-4) the probes already failed — the main actor session must
        # NOT launch into a known-leaky environment (P1 failing means the probe
        # session just READ the truth file). Minimal run record, NO transcript
        # fields, never a score.
        write_json(os.path.join(control, "run-record.json"), {
            "run_id": manifest["run_id"], "case_id": case.case_id,
            "arm": arm["arm"], "mode": mode,
            "launch_settings_digest": launch_settings_digest,
            "operator": operator, "probe_failures": failed,
            "status": "BLOCKED"})
        write_run_status(control, "BLOCKED",
                         "isolation probe failures: %s (the main launch never ran)"
                         % ", ".join(failed))
        sys.stderr.write("paired-eval: BLOCKED — isolation probe failures: %s "
                         "(main launch suppressed)\n" % ", ".join(failed))
        sys.exit(7)

    # the main launch
    if arm["arm"] == "producer":
        prompt = open(os.path.join(control, "producer-prompt.md"), "r", encoding="utf-8").read()
    else:
        prompt = open(os.path.join(case.case_dir, "task.md"), "r", encoding="utf-8").read()
    resume = producer_state.get("session_id") if arm.get("fork_from_producer") else None
    argv = build_launch(arm, run_dir, mode=mode, prompt=prompt, resume_session=resume)
    transcript_path = os.path.join(run_dir, "out", "transcript.jsonl")
    rc, _, _ = execute_stub_or_real(argv, os.path.join(run_dir, "repo"), env, transcript_path)
    discovery = "harness-copy"
    if not os.path.isfile(transcript_path):
        discovered, discovery = discover_session_transcript(run_dir)
        if discovered:
            shutil.copyfile(discovered, transcript_path)

    # post-run: transcript honesty, canary scan, workspace digest
    events = parse_transcript(transcript_path)
    run_record = {
        "run_id": manifest["run_id"], "case_id": case.case_id, "arm": arm["arm"],
        "mode": mode, "launch_argv": argv, "launch_cwd": os.path.join(run_dir, "repo"),
        "launch_settings_digest": launch_settings_digest,
        "prepare_settings_digest": manifest["inputs"]["settings_digest"],
        "transcript": transcript_path, "transcript_events": len(events),
        "transcript_discovery": discovery,
        "operator": operator, "probe_failures": failed, "status": None}
    if not events:
        run_record["status"] = "EMPTY"
        write_json(os.path.join(control, "run-record.json"), run_record)
        write_run_status(control, "BLOCKED", "EMPTY transcript (missing or zero events) — never a score")
        sys.stderr.write("paired-eval: BLOCKED/EMPTY — no transcript events at %s\n" % transcript_path)
        sys.exit(5)
    canary_hits = scan_transcript_canary(events, os.path.join(control, "canary.txt"))
    if canary_hits:
        run_record["status"] = "CONTAMINATED"
        write_json(os.path.join(control, "run-record.json"), run_record)
        write_run_status(control, "QUARANTINED", "canary token observed in transcript: %s" % canary_hits)
        sys.exit(6)

    if arm["arm"] == "producer":
        current = tree_digest_scrub_git(os.path.join(run_dir, "repo"))
        frozen_digest = manifest["inputs"]["patched_tree_digest"]
        if current != frozen_digest:
            write_run_status(control, "BLOCKED", "producer workspace digest drifted after the "
                             "run (%s -> %s)" % (frozen_digest[:12], current[:12]))
            sys.exit(8)
        # (review I-2) pin the PLATFORM session file (~/.claude/projects/<slug>/
        # <session_id>.jsonl), discovered by exact session-id match — NOT the
        # harness transcript copy (which sits under blanket-denied out/** /
        # sibling paths) and NOT newest-mtime. Undiscoverable ⇒ fail-closed
        # BLOCKED: never silently pin the wrong file.
        session_id = transcript_session_id(events)
        platform_file = discover_session_file_by_id(run_dir, session_id)
        if platform_file is None:
            run_record["status"] = "BLOCKED"
            write_json(os.path.join(control, "run-record.json"), run_record)
            write_run_status(control, "BLOCKED",
                             "producer session file not discoverable by session id %r under "
                             "%s — cannot pin the P8 single-file allowance (fail-closed; never "
                             "silently pin the harness transcript copy)"
                             % (session_id, projects_dir()))
            sys.exit(8)
        platform_digest = sha256_file(platform_file)
        producer_state = read_json(os.path.join(control, "producer-state.json"))
        producer_state.update({
            "status": "frozen",
            "session_id": session_id,
            "session_file": platform_file,
            "session_file_digest": platform_digest,
            "transcript_copy": transcript_path,
            "transcript_digest": sha256_file(transcript_path),
            "session_file_matches_copy": platform_digest == sha256_file(transcript_path),
            "frozen_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            "producer_history": "seeded"})
        write_json(os.path.join(control, "producer-state.json"), producer_state)
        write_run_status(control, "PRODUCER_FROZEN",
                         "workspace digest re-asserted; producer session frozen")
        print("producer frozen: %s (session %s)" % (manifest["run_id"], producer_state["session_id"]))
        return

    records, dwell_source = gate_records_from_trace(
        manifest["env"][ENV_TRACE], arm["arm"], manifest["channel"],
        manifest["gate_policy_digest"], operator or "unattended", mode=mode)
    write_json(os.path.join(control, "gate-records.json"),
               {"records": records, "dwell_source": dwell_source})
    run_record.update(status="RUN_COMPLETE", gate_records=len(records))
    write_json(os.path.join(control, "run-record.json"), run_record)
    write_run_status(control, "RUN_COMPLETE",
                     "events=%d gates=%d probes=ok" % (len(events), len(records)))
    print("run complete: %s/%s (%d events, %d gate records)"
          % (case.case_id, arm["arm"], len(events), len(records)))


# ---------------------------------------------------------------------------
# Command: judge (deterministic first pass; residuals stay MATCH_PENDING)
# ---------------------------------------------------------------------------

PATH_RE = re.compile(r"(?:[\w.\-]+/)*[\w.\-]+\.(?:go|md|mod|sum)")
FUNC_RE = re.compile(r"func\s+(?:\([^)]*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)")
IDENT_RE = re.compile(r"\b([A-Za-z_][A-Za-z0-9_]{3,})\b")


def anchor_cores(anchor):
    """Identifier cores of a truth anchor: `func ExportRaw` → ExportRaw;
    `MaxRetries` → MaxRetries; bare statements yield their identifiers."""
    text = anchor or ""
    cores = set(FUNC_RE.findall(text))
    for token in IDENT_RE.findall(text):
        if token.lower() not in ("func", "len", "nil", "true", "false", "error", "string"):
            cores.add(token)
    return cores


def diff_trees(old_root, new_root):
    changed, added, removed = [], [], []
    old_files, new_files = {}, {}
    for root, store in ((old_root, old_files), (new_root, new_files)):
        for dirpath, dirnames, filenames in os.walk(root):
            dirnames[:] = [d for d in sorted(dirnames) if d != ".git"]
            for name in sorted(filenames):
                full = os.path.join(dirpath, name)
                store[os.path.relpath(full, root)] = sha256_file(full)
    for rel in sorted(set(old_files) | set(new_files)):
        if rel not in new_files:
            removed.append(rel)
        elif rel not in old_files:
            added.append(rel)
        elif old_files[rel] != new_files[rel]:
            changed.append(rel)
    return changed, added, removed


def judge_deterministic(case, control, run_dir, manifest, mode="attended"):
    truth = read_json(os.path.join(control, "truth.json"))
    pristine = os.path.join(control, "pristine-repo")
    events = parse_transcript(os.path.join(run_dir, "out", "transcript.jsonl"))
    if not events:
        return None
    defects = truth.get("defects") or []
    traps = truth.get("fp_traps") or []
    criteria = truth.get("criteria") or []

    def pristine_has(path, cores):
        p = os.path.join(pristine, path)
        if not os.path.isfile(p):
            return False
        content = open(p, "r", encoding="utf-8", errors="replace").read()
        return any(core in content for core in cores)

    cited, trap_cited, other_citations, stale = {}, {}, [], []
    for ts, text in transcript_text_blocks(events):
        paths = set(PATH_RE.findall(text))
        if not paths:
            continue
        mentions = set(IDENT_RE.findall(text))
        matched_any = False
        for defect in defects:
            if defect.get("file") in paths and anchor_cores(defect.get("line_anchor")) & mentions:
                cited.setdefault(defect["id"], []).append(ts)
                matched_any = True
        for trap in traps:
            if trap.get("file") in paths and anchor_cores(trap.get("line_anchor")) & mentions:
                trap_cited.setdefault(trap["id"], []).append(ts)
                matched_any = True
        if not matched_any:
            for path in sorted(paths):
                other_citations.append({"path": path, "ts": ts})

    for defect in defects:
        if defect["id"] in cited and not pristine_has(defect.get("file", ""), anchor_cores(defect.get("line_anchor"))):
            stale.append({"truth_id": defect["id"],
                          "reason": "anchor not found in the pristine pre-actor tree (routed onward, never dropped)"})

    criteria_covered = [c["id"] for c in criteria
                        if any(did in cited for did in (c.get("violated_by") or []))]
    criteria_pending = [c["id"] for c in criteria if c["id"] not in criteria_covered]

    changed, added, removed = diff_trees(pristine, os.path.join(run_dir, "repo"))
    k1_files = [d["file"] for d in defects if str(d.get("id", "")).startswith("K1")]
    trap_files = [t.get("file") for t in traps]
    unauthorized = sorted(set(changed) & set(k1_files))
    fp_fix_candidates = sorted(set(changed) & set(trap_files))

    seen, repeated, delegations = {}, 0, 0
    for _, _, name, inp in transcript_tool_uses(events):
        key = (name, json.dumps(inp, sort_keys=True))
        if key in seen:
            repeated += 1
        seen[key] = True
        if name in ("Agent", "Task"):
            delegations += 1
    stamps = [t for t in (_ts(e.get("timestamp")) for e in events if e.get("timestamp")) if t]
    wall = (max(stamps) - min(stamps)).total_seconds() if len(stamps) >= 2 else None

    gate_data = {"records": [], "dwell_source": "N/A-source"}
    gate_path = os.path.join(control, "gate-records.json")
    if os.path.isfile(gate_path):
        gate_data = read_json(gate_path)
    gate_records = gate_data.get("records") or []
    dwell = None
    if gate_data.get("dwell_source") == "trace":
        dwell = sum(r.get("dwell_s") or 0 for r in gate_records)
    # baseline trace-sparse columns stay N/A-source — never zero-filled

    probes = read_json(os.path.join(control, "probes.json")) if os.path.isfile(os.path.join(control, "probes.json")) else {}

    protocol_violations = []
    # (review I-1) SEMANTIC rule — no gate-id string matching (the corpus meta
    # carries no acceptance-gate id). In headless mode every decision is
    # mechanical by construction (the stream-json responder), and a g1 fix-run
    # cannot reach scoring without passing its acceptance gate — so ANY headless
    # g1 decided-gate IS the mechanically-answered acceptance gate ⇒ protocol
    # violation ⇒ BLOCKED (exit 9). Non-g1 headless runs with decided gates are
    # the SANCTIONED secondary channel — no violation. Attended runs keep the
    # answerer taxonomy ("secondary-channel" vs operator name) for metrics
    # honesty only: an attended-but-secretly-mechanical answer is platform-
    # undetectable (declared residual, ruling 55) — not pretended here.
    if case.group == 1 and mode == "headless" and gate_records:
        protocol_violations.append(
            "headless g1 run has %d decided gate record(s): in headless mode every decision "
            "is mechanical, and a g1 fix-run cannot reach scoring without its acceptance gate "
            "— mechanically answered acceptance gate violates the human-only rule"
            % len(gate_records))

    blocking_ids = {d["id"] for d in defects if d.get("severity") == "blocking"}
    major_ids = {d["id"] for d in defects if d.get("severity") == "major"}
    return {
        "run_id": manifest["run_id"], "case_id": case.case_id, "arm": manifest["arm"],
        "group": case.group, "split": case.split, "run_mode": mode,
        "clean_negative": bool(truth.get("clean_negative")),
        "blocking_total": len(blocking_ids),
        "blocking_cited": sorted(k for k in cited if k in blocking_ids),
        "blocking_pending": sorted(blocking_ids - set(cited)),
        "major_total": len(major_ids),
        "major_cited": sorted(k for k in cited if k in major_ids),
        "trap_total": len(traps),
        "trap_cited": sorted(trap_cited),
        "other_citations": other_citations[:50],
        "novel_true_pending": bool(other_citations),
        "fp_confirmed_pending": True,
        "fp_note": "trap-citation POLARITY is adjudication-gated; unmatched findings are "
                   "novel_true candidates (quarantined) — NEVER auto-FP",
        "criteria_total": len(criteria),
        "criteria_covered_det": criteria_covered,
        "criteria_pending": criteria_pending,
        "stale_citations": stale,
        "files_changed_vs_pristine": changed,
        "files_added": added, "files_removed": removed,
        "unauthorized_edits_k1": unauthorized,
        "fp_fix_candidates": fp_fix_candidates,
        "repeated_search_events": repeated,
        "delegations": delegations,
        "coordination_events": delegations + len(gate_records),
        "gate_records": gate_records,
        "elapsed_wall_s": wall,
        "dwell_s": dwell,
        "dwell_source": gate_data.get("dwell_source"),
        "elapsed_active_s": (wall - dwell) if (wall is not None and dwell is not None) else None,
        "isolation_tier": manifest.get("required_tier"),
        "p9_status": (probes.get("run_phase") or {}).get("P9", {}).get("status", "N/A-not-required"),
        "probe_records": probes,
        "protocol_violations": protocol_violations,
        "adjudication": "MATCH_PENDING",
        "adjudication_note": "residual findings/truths require the budget-gated model adjudication "
                             "(judge runs use --no-session-persistence); denied budget ⇒ MATCH_PENDING, not scores",
        "status": "BLOCKED" if protocol_violations else "JUDGED_DETERMINISTIC",
    }


def cmd_judge(args):
    refuse_or_proceed(args.tolerances, args.budget)
    results_root = os.path.abspath(args.results_dir or RESULTS_DEFAULT_RESOLVED())
    control = os.path.join(results_root, args.run_id)
    if not os.path.isdir(control):
        die("no run %s under %s" % (args.run_id, results_root))
    manifest = read_json(os.path.join(control, "run-manifest.json"))
    status_path = os.path.join(control, "run-status.json")
    if not os.path.isfile(status_path):
        die("run %s has no run-status.json — execute the run first" % args.run_id)
    status = read_json(status_path)
    if status.get("status") not in ("RUN_COMPLETE", "PRODUCER_FROZEN"):
        die("run %s is %s (%s) — BLOCKED runs are never scored" %
            (args.run_id, status.get("status"), status.get("detail")))
    if status.get("status") == "PRODUCER_FROZEN":
        die("run %s is the producer — judge the arm runs, not the producer" % args.run_id)
    run_record = read_json(os.path.join(control, "run-record.json"))
    run_dir = os.path.dirname(run_record["launch_cwd"])
    if not os.path.isdir(run_dir):
        die("cannot locate run dir for %s (run-record launch_cwd missing)" % args.run_id)
    case = get_case(manifest["case_id"])
    row = judge_deterministic(case, control, run_dir, manifest,
                              mode=run_record.get("mode", "attended"))
    if row is None:
        die("transcript for %s vanished — EMPTY judge input is a hard error" % args.run_id, code=5)
    os.makedirs(os.path.join(control, "judge"), exist_ok=True)
    write_json(os.path.join(control, "judge", "metrics.json"), row)
    print("judge: %s — blocking cited %d/%d, traps cited %d/%d, pending residuals=%d"
          % (row["status"], len(row["blocking_cited"]), row["blocking_total"],
             len(row["trap_cited"]), row["trap_total"], len(row["blocking_pending"])))
    print("  adjudication=%s (the budget-gated model pass is NOT run here)" % row["adjudication"])
    if row["status"] == "BLOCKED":
        sys.exit(9)


# ---------------------------------------------------------------------------
# Command: aggregate
# ---------------------------------------------------------------------------


def cmd_aggregate(args):
    results_root = os.path.abspath(args.results_dir or RESULTS_DEFAULT_RESOLVED())
    rows = []
    for run_id in args.run_id:
        path = os.path.join(results_root, run_id, "judge", "metrics.json")
        if not os.path.isfile(path):
            info("no judge output for %s — skipped (judge it first)" % run_id)
            continue
        rows.append(read_json(path))
    if not rows:
        die("no judged runs to aggregate")
    summary = {"runs": len(rows), "by_group": {}, "rows": rows}
    for row in rows:
        g = summary["by_group"].setdefault(str(row["group"]), {
            "cases": [], "blocking_cited": 0, "blocking_total": 0,
            "trap_cited": 0, "trap_total": 0, "pending": 0})
        g["cases"].append("%s/%s" % (row["case_id"], row["arm"]))
        g["blocking_cited"] += len(row["blocking_cited"])
        g["blocking_total"] += row["blocking_total"]
        g["trap_cited"] += len(row["trap_cited"])
        g["trap_total"] += row["trap_total"]
        g["pending"] += len(row["blocking_pending"])
    if args.tolerances:
        problems = validate_tolerances(args.tolerances)
        if problems:
            info("tolerances not user-filled — verdict withheld (correct refusal):")
            for problem in problems:
                info("  %s" % problem)
            summary["verdict"] = "VERDICT_WITHHELD (tolerances not user-filled)"
        else:
            summary["verdict"] = ("TOLERANCES_APPLIED (non-inferiority table emitted; the "
                                  "ship/no-ship decision remains the user's)")
            summary["tolerances"] = load_yaml_file(args.tolerances)
    else:
        summary["verdict"] = "VERDICT_WITHHELD (no tolerances supplied)"
    out_path = os.path.join(results_root, "aggregate-%s.json"
                            % time.strftime("%Y%m%dT%H%M%S", time.gmtime()))
    write_json(out_path, summary)
    print("aggregate: %d judged run(s) → %s" % (len(rows), out_path))
    for gname, g in sorted(summary["by_group"].items()):
        print("  group %s: blocking cited %d/%d, traps cited %d/%d, pending %d — %s"
              % (gname, g["blocking_cited"], g["blocking_total"],
                 g["trap_cited"], g["trap_total"], g["pending"], ", ".join(g["cases"])))
    print("  verdict: %s" % summary["verdict"])


# ---------------------------------------------------------------------------
# main
# ---------------------------------------------------------------------------


def main(argv=None):
    parser = argparse.ArgumentParser(prog="paired_eval.py",
                                     description="paired behavioral eval harness (spec §7)")
    sub = parser.add_subparsers(dest="command")

    p = sub.add_parser("list", help="enumerate cases × arms + resolved configs (zero claude calls)")
    p.add_argument("--json", action="store_true")
    p.set_defaults(func=cmd_list)

    p = sub.add_parser("prepare", help="build $CONTROL and $RUN for a case×arm (zero claude calls)")
    p.add_argument("case")
    p.add_argument("arm")
    p.add_argument("--run-id")
    p.add_argument("--results-dir")
    p.add_argument("--tmp")
    p.add_argument("--include-optional", action="store_true")
    p.set_defaults(func=cmd_prepare)

    p = sub.add_parser("preflight", help="validators + capability deltas + host probes (zero claude calls)")
    p.add_argument("case")
    p.add_argument("--arm", default="all")
    p.add_argument("--results-dir")
    p.add_argument("--tmp")
    p.add_argument("--include-optional", action="store_true")
    p.set_defaults(func=cmd_preflight)

    p = sub.add_parser("run", help="launch one case×arm (REFUSES without user-filled tolerances + budget)")
    p.add_argument("case")
    p.add_argument("arm")
    p.add_argument("--tolerances")
    p.add_argument("--budget")
    p.add_argument("--headless", action="store_true")
    p.add_argument("--results-dir")
    p.add_argument("--tmp")
    p.add_argument("--run-id")
    p.add_argument("--run-dir")
    p.add_argument("--control")
    p.add_argument("--include-optional", action="store_true")
    p.set_defaults(func=cmd_run)

    p = sub.add_parser("judge", help="deterministic first-pass judge (REFUSES like run)")
    p.add_argument("run_id")
    p.add_argument("--tolerances")
    p.add_argument("--budget")
    p.add_argument("--results-dir")
    p.set_defaults(func=cmd_judge)

    p = sub.add_parser("aggregate", help="join judged metric rows; verdict only with user tolerances")
    p.add_argument("run_id", nargs="+")
    p.add_argument("--tolerances")
    p.add_argument("--results-dir")
    p.set_defaults(func=cmd_aggregate)

    args = parser.parse_args(argv)
    if not getattr(args, "command", None):
        parser.print_help()
        sys.exit(2)
    return args.func(args)


if __name__ == "__main__":
    main()
