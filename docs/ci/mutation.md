# Schedule weekly mutation tests

The reusable mutation workflow runs `make mutation` in a consumer repository and publishes the score. A reusable workflow cannot own a schedule for its caller. The consumer's own workflow sets the cron trigger and invokes the reusable workflow in a job.

## Add the weekly caller

1. Create `.github/workflows/mutation.yml` in the consumer repository.
2. Set `on.schedule` to a weekly cron expression and add `workflow_dispatch` for manual runs.
3. Add one job that uses `agoodkind/go-makefile/.github/workflows/_mutation.yml@main`.
4. Set the inputs under `with`. Every input is optional.

| Input | Default | Effect |
| --- | --- | --- |
| `packages` | `.` | Space-separated directories to mutate. Gremlins runs once per directory. |
| `min_score` | empty | Percentage from 0 to 100. The run fails when killed / (killed + lived) falls below it. Empty reports the score without failing. |
| `timeout_coefficient` | `20` | Multiplier on each mutant's test timeout. |
| `flags` | empty | Extra `gremlins unleash` flags. |
| `working_directory` | `.` | Directory where make runs. |
| `runs_on` | `ubuntu-latest` | Runner label. |

```yaml
name: Mutation

on:
  schedule:
    - cron: "0 6 * * 1"
  workflow_dispatch:

permissions:
  contents: read

jobs:
  mutation:
    uses: agoodkind/go-makefile/.github/workflows/_mutation.yml@main
    permissions:
      contents: read
    with:
      packages: "./internal/parser ./internal/store"
      min_score: "60"
      timeout_coefficient: "30"
```

The run writes the Markdown summary to the run page and uploads the `mutation-report` artifact with the JSON report and the summary.

## Read the score

The score is killed / (killed + lived). Timed-out mutants do not count toward it. A high timeout count means the score covers fewer mutants. Raise `timeout_coefficient` to extend each mutant's timeout so slow mutants finish as killed or lived.

## Run mutation tests locally

Run `make mutation` in the repository. Set `MUTATION_PACKAGES` to limit the directories and `MUTATION_MIN_SCORE` to fail below a score.

```sh
MUTATION_PACKAGES="./internal/parser" MUTATION_MIN_SCORE=60 make mutation
```

The target writes the combined JSON report to `.make/mutation-report.json` and the Markdown summary to `.make/mutation-summary.md`, then prints the summary.
