# Deepzora rename: CodeQL baseline comparison

This is a historical comparison for PR #13, not a dismissal of security alerts.
CodeQL scanning and its existing findings remain enabled.

The base is `93ae80fb7ec23f7ebe99e24f33952cdcbe0d4764` (main before the rename).
The compared rename head is `45e0d622e4603384b5a8366ce86cb6d28e40a193`.
The independent CodeQL check reported 85 alerts in changed code, with a warning
that a large diff can include alerts not introduced by the PR. All 85 annotations
map to existing open main alerts by rule description, source path and start line.
The existing main baseline contains 241 open alerts; none were dismissed here.

| Report category | Annotations matched to existing main alerts |
| --- | ---: |
| Uncontrolled data used in path expression | 78 |
| Clear-text logging of sensitive information | 4 |
| Shell command built from environment values | 1 |
| Size computation for allocation may overflow | 1 |
| Uncontrolled data used in network request | 1 |
| Total | 85 |

These annotations cover 34 files. Each entire file is equivalent to its base
version after reversing only the authorized brand substitutions and normalizing
whitespace: `deepzora`/`Deepzora`/`DEEPZORA` to their Deeper forms,
`deepzora.com` to `deeper.io`, and `com.deepzora.desktop` to the former application
ID. No annotated file required another transformation to match. Thus the rename
does not introduce or repair the reported operations. Existing alerts, including
the allocation-size warning, require their own security triage; this comparison
does not classify them as false positives.

The review decision retains CodeQL and the seven protected functional checks.
The rename can be evaluated against its unchanged security baseline after those
checks and the analysis jobs complete. The independent CodeQL report can remain
red because of existing alerts in the broad rename diff; it must not be described
as a passing security check or suppressed to make the PR appear green.

本次对照仅证明这些告警不是改名新引入的缺陷；没有关闭、忽略或降低任何告警。
功能 CI、平台测试及扫描执行结果与安全告警报告分别记录。
