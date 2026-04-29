## What
Provide a concise description of the changes:
* What changes have you made? (High-level overview)
* What does it mean to the user? (In plain English)

Example:
> * Added consistent-hash rebalancing on node leave/join.
> * This means traffic is now redistributed gracefully without a full blocklist flush.

## Why
Explain why the changes are necessary:
* Why were these changes made?
* What's the benefit?

Example:
> * Node churn caused brief rate-limit bypasses because the leaving node's counters
>   were not handed off before shutdown.
> * This fix ensures the blocklist state is propagated before the node exits.

## References
Link any supporting context or documentation:
* GitHub issues, documentation, helpful links.
* Use `closes #123` if this PR closes a GitHub issue `#123`.
