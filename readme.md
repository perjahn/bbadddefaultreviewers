# bbadddefaultreviewers

A command-line tool that resolves users from Active Directory groups and users, and adds them as Bitbucket default reviewers for a repo.

## Requirements

- Go 1.27.2 or newer
- Active Directory ldap access
- A Bitbucket server/data center account with permission to manage repository default reviewers
- The Bitbucket default reviewers rest resource enabled at `/rest/default-reviewers/1.0`

## Usage

```sh
export BITBUCKET_USERNAME='automation-user'
export BITBUCKET_TOKEN='bitbucket-personal-access-token'

go run . \
  --ldap-url 'ldaps://ad.example.com:636' \
  --ldap-base-dn 'DC=example,DC=com' \
  --repo-url 'https://bitbucket.example.com/projects/MYPROJ/repos/myrepo' \
  --repo-url 'https://bitbucket.example.com/projects/OTHER/repos/another-repo' \
  --target-branch 'master' \
  --required-approvers 2 \
  Group1 Group2 User1 User2
```

Specify `--repo-url` once per repository. All repository URLs must use the same Bitbucket server and context path. Single-run mode is the default. To keep synchronizing in a loop, add `--sleep 60` to run forever and wait 60 seconds between each sync pass.

```sh
go run . \
  --ldap-url 'ldaps://ad.example.com:636' \
  --ldap-base-dn 'DC=example,DC=com' \
  --repo-url 'https://bitbucket.example.com/projects/MYPROJ/repos/myrepo' \
  --target-branch 'master' \
  --required-approvers 2 \
  --sleep 60 \
  Group1 Group2 User1 User2
```

In loop mode, failed sync attempts are logged and the loop continues retrying. Only an explicit stop signal ends the loop.

LDAP authentication is optional; when required, set both `LDAP_BIND_DN` and `LDAP_PASSWORD`. Bitbucket authentication always uses `BITBUCKET_USERNAME` and `BITBUCKET_TOKEN` as HTTP basic authentication credentials.

Pass `--dry-run` to fetch the matching condition and print the planned reviewers, branch, and approval changes without sending a create or update request. The same diff is printed before writes during a normal run.

Repository URLs can use either `/projects/{projectKey}/repos/{repoSlug}` or the clone form `/scm/{projectKey}/{repoSlug}.git`. A Bitbucket context path, such as `/bitbucket`, is preserved.

Group and user arguments are matched against Active Directory `sAMAccountName`. Only direct group members are resolved; nested groups are not traversed. User `sAMAccountName` values are sent to Bitbucket as reviewer names. Duplicate users across groups are added once. AD users missing in Bitbucket will be ignored, not added as default reviewers.

## Environment variables

The app also accepts several configuration values via environment variables when the corresponding CLI flag is not supplied:

- `LDAP_URL`
- `LDAP_BASE_DN`
- `REPO_URL`
- `SOURCE_BRANCH`
- `TARGET_BRANCH`
- `REQUIRED_APPROVERS`
- `SLEEP`
- `DRY_RUN`
- `GROUPS_OR_USERS`
- `BITBUCKET_USERNAME`
- `BITBUCKET_TOKEN`
- `LDAP_BIND_DN`
- `LDAP_PASSWORD`

Example:

```sh
export LDAP_URL='ldaps://ad.example.com:636'
export LDAP_BASE_DN='DC=example,DC=com'
export REPO_URL='https://bitbucket.example.com/projects/MYPROJ/repos/myrepo;https://bitbucket.example.com/projects/OTHER/repos/another-repo'
export TARGET_BRANCH='master'
export REQUIRED_APPROVERS='2'
export GROUPS_OR_USERS='Group1,Group2,User1'
export DRY_RUN='true'

go run .
```

CLI flags still override environment values when both are provided.
`REPO_URL` accepts multiple repository URLs separated by commas or semicolons. Repeated `--repo-url` flags can be used instead; if any are supplied, they replace `REPO_URL`.

## Bitbucket behavior

The tool uses the default reviewers conditions api. It creates a repository condition with `POST .../repos/{repo}/condition`, or updates the existing condition with `PUT .../repos/{repo}/condition/{id}` when both source and target branch match. The request includes `sourceMatcher`, `targetMatcher`, `reviewers`, and `requiredApprovals`:

```json
{
  "reviewers": [{"name": "username"}],
  "sourceMatcher": {"id": "ANY_REF_MATCHER_ID", "displayId": "ANY_REF_MATCHER_ID", "type": {"id": "ANY_REF", "name": "Any branch"}, "active": true},
  "targetMatcher": {"id": "refs/heads/master", "displayId": "master", "type": {"id": "BRANCH", "name": "Branch"}, "active": true},
  "requiredApprovals": 2
}
```

or

```json
{
  "reviewers": [{"name": "username"}],
  "sourceMatcher": {"id": "refs/heads/feature-123", "displayId": "feature-123", "type": {"id": "BRANCH", "name": "Branch"}, "active": true},
  "targetMatcher": {"id": "refs/heads/master", "displayId": "master", "type": {"id": "BRANCH", "name": "Branch"}, "active": true},
  "requiredApprovals": 2
}
```

Both branch options are optional. A supplied branch name is sent as an exact branch matcher like `refs/heads/mybranch`; an omitted or empty branch is treated as an any-branch. The tool updates a condition only when its matcher type matches and, for `BRANCH` matchers, its ID or DisplayID matches the supplied branch name. An `ANY_REF` matcher matches only when the corresponding branch option is empty.

The endpoint schema follows Atlassian's archived [Bitbucket Server default reviewers rest reference](https://docs.atlassian.com/bitbucket-server/rest/7.21.0/bitbucket-default-reviewers-rest.html), which documents repository conditions, `requiredApprovals`, and the create/update methods.

## Tests

```sh
go test ./...
```
