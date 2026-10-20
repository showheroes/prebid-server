# Copilot review instructions

## Jira & Confluence context

- If the PR title or branch contains a Jira key (PLT-<n>), fetch the issue with getJiraIssue
  (cloudId `c8f11040-fa34-4c9e-8450-1e366125769a`) and check that the changes match its requirements.
- If the issue links Confluence pages (in the description or remote links), read the relevant ones
  with getConfluencePage for additional context.
