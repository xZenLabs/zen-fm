#!/bin/sh
set -eu

[ "$#" -eq 8 ] || {
    echo "usage: fetch-release-evidence.sh REPOSITORY DEFAULT_BRANCH COMMIT QEMU_RUN K4_RUN K5_RUN PW1_RUN OUTPUT_DIR" >&2
    exit 2
}
command -v gh >/dev/null 2>&1 || { echo "gh is required" >&2; exit 2; }
command -v jq >/dev/null 2>&1 || { echo "jq is required" >&2; exit 2; }

REPOSITORY=$1
DEFAULT_BRANCH=$2
COMMIT=$3
QEMU_RUN=$4
K4_RUN=$5
K5_RUN=$6
PW1_RUN=$7
OUTPUT=$8
mkdir -p "$OUTPUT"
[ -z "$(find "$OUTPUT" -mindepth 1 -maxdepth 1 -print -quit)" ] || {
    echo "evidence output directory must be empty" >&2
    exit 2
}

fetch() {
    run_id=$1
    expected_workflow=$2
    expected_path=$3
    artifact=$4
    response=$(gh api "repos/$REPOSITORY/actions/runs/$run_id")
    printf '%s' "$response" | jq -e \
        --arg workflow "$expected_workflow" --arg path "$expected_path" \
        --arg branch "$DEFAULT_BRANCH" --arg repository "$REPOSITORY" '
          .name == $workflow and
          .path == $path and
          .event == "workflow_dispatch" and
          .head_branch == $branch and
          .head_repository.full_name == $repository and
          .conclusion == "success"
        ' >/dev/null || {
            echo "qualification run $run_id is not a successful protected $expected_workflow run from $DEFAULT_BRANCH" >&2
            exit 1
        }
    gh run download "$run_id" --repo "$REPOSITORY" --name "$artifact" --dir "$OUTPUT"
}

fetch "$QEMU_RUN" 'Old-kernel QEMU qualification' '.github/workflows/old-kernel-qemu.yml' \
    "old-kernel-qemu-evidence-$COMMIT"
fetch "$K4_RUN" 'Physical Kindle qualification' '.github/workflows/physical-kindle.yml' \
    "physical-kindle4-evidence-$COMMIT"
fetch "$K5_RUN" 'Physical Kindle qualification' '.github/workflows/physical-kindle.yml' \
    "physical-kindle5-evidence-$COMMIT"
fetch "$PW1_RUN" 'Physical Kindle qualification' '.github/workflows/physical-kindle.yml' \
    "physical-paperwhite1-evidence-$COMMIT"
