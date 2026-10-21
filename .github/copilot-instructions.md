# Copilot review instructions

## Jira & Confluence context

- Identify the Jira ticket for this PR: first look for a link to
  `https://showheroes.atlassian.net/browse/<KEY>` in the PR description; if there
  is none, look for a ticket key (almost always `PLT-<number>`) in the PR title,
  description or branch name.
- Fetch the ticket with `getJiraIssue` (cloudId `c8f11040-fa34-4c9e-8450-1e366125769a`) and
  compare the changes with its description and acceptance criteria. Point out
  clear mismatches (missing requirements, changes unrelated to the ticket), but
  don't flag work the ticket explicitly leaves to other tickets.
- If the ticket links Confluence pages (in its description or in its remote
  links), read them with `getConfluencePage` as additional context. Only read
  pages linked directly from the ticket; don't follow links found inside them.
- If the ticket or a page can't be fetched, continue the review without it and
  don't guess its content.
- In the review summary, add a short "Jira context" note stating:
  - the ticket key used and where it was found (description link, title,
    description text or branch name), or that no ticket was found;
  - the Confluence pages read (title or URL);
  - any ticket or page that could not be fetched, with the error if available.
