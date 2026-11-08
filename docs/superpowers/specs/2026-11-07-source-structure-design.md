# Enforce Go command structure and formatting

- Keep `cmd/*` limited to delegation and exit status. Put parsing, setup, and
  implementation in importable packages. Allow ordinary error handling and
  preserve the existing error-wrapping and boundary-logging requirements.
- Keep short calls and simple declarations on one line. Wrap declarations with
  callback parameters, including named function types. Format wrapped lists with one argument or parameter
  per line. Apply the supplied `wsl_v5` settings through `make fmt`.
- Detect repeated prefixes with or without separators. Require two distinct
  filename stems and at least three shared characters.
- Exclude ordinary test and platform suffixes from prefix comparisons. Report
  required directory changes without automatically renaming files.
- Use applied notices to baseline eligible historical violations and reject
  later violations. Preserve the published effective dates and distinguish
  repeated declarations in the same file.

Apply these `wsl_v5` settings:

```yaml
wsl_v5:
  default: none
  enable:
    - after-block
    - return
    - err
    - for
    - range
  allow-first-in-block: false
  allow-whole-block: false
  branch-max-lines: 2
  case-max-lines: 0
  cuddle-max-statements: 1
```
