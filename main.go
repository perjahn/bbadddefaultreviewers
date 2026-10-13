package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var errStopLoop = errors.New("stop loop")

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("bbadddefaultreviewers", flag.ContinueOnError)
	ldapUrl := flags.String("ldap-url", os.Getenv("LDAP_URL"), "LDAP or LDAPS server url")
	ldapBaseDN := flags.String("ldap-base-dn", os.Getenv("LDAP_BASE_DN"), "LDAP search base DN")
	repoUrls := repoUrlFlag{values: splitValues(os.Getenv("REPO_URL"))}
	flags.Var(&repoUrls, "repo-url", "Bitbucket Server repository url; may be specified multiple times")
	sourceBranch := flags.String("source-branch", os.Getenv("SOURCE_BRANCH"), "Source branch for the default reviewer condition")
	targetBranch := flags.String("target-branch", os.Getenv("TARGET_BRANCH"), "Target branch for the default reviewer condition")
	requiredApprovers := flags.Int("required-approvers", intEnvOrDefault("REQUIRED_APPROVERS", 1), "Minimum required approvals")
	sleepSeconds := flags.Int("sleep", intEnvOrDefault("SLEEP", 0), "Run forever, sleeping this many seconds between iterations; 0 disables looping")
	dryRun := flags.Bool("dry-run", boolEnvOrDefault("DRY_RUN", false), "Show the reviewer changes without updating Bitbucket")
	if err := flags.Parse(args); err != nil {
		return err
	}
	groupsAndUsers := flags.Args()
	if len(groupsAndUsers) == 0 {
		groupsAndUsers = splitValues(os.Getenv("GROUPS_OR_USERS"))
	}
	if strings.TrimSpace(*ldapUrl) == "" || strings.TrimSpace(*ldapBaseDN) == "" || len(repoUrls.values) == 0 || len(groupsAndUsers) == 0 {
		return fmt.Errorf("usage: bbadddefaultreviewers --ldap-url ldap_url --ldap-base-dn base_dn --repo-url repository_url [--repo-url another_repository_url] [--source-branch branch] [--target-branch branch] [--required-approvers n] [--sleep seconds] [--dry-run] group_or_user [group_or_user...]")
	}
	if *requiredApprovers < 0 {
		return fmt.Errorf("required approvers cannot be negative")
	}
	if *sleepSeconds < 0 {
		return fmt.Errorf("sleep cannot be negative")
	}

	if *sleepSeconds > 0 {
		return runLoop(*sleepSeconds, func() error {
			return runOnce(*ldapUrl, *ldapBaseDN, repoUrls.values, *sourceBranch, *targetBranch, *requiredApprovers, *dryRun, groupsAndUsers)
		})
	}
	return runOnce(*ldapUrl, *ldapBaseDN, repoUrls.values, *sourceBranch, *targetBranch, *requiredApprovers, *dryRun, groupsAndUsers)
}

type repoUrlFlag struct {
	values  []string
	changed bool
}

func (flagValue *repoUrlFlag) String() string {
	return strings.Join(flagValue.values, ",")
}

func (flagValue *repoUrlFlag) Set(value string) error {
	if !flagValue.changed {
		flagValue.values = nil
		flagValue.changed = true
	}
	flagValue.values = append(flagValue.values, value)
	return nil
}

func splitValues(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || unicode.IsSpace(r)
	})
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func intEnvOrDefault(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		log.Printf("ignoring invalid integer value for %s=%q: %v", key, value, err)
		return fallback
	}
	return parsed
}

func boolEnvOrDefault(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		log.Printf("ignoring invalid bool value for %s=%q: %v", key, value, err)
		return fallback
	}
	return parsed
}

func runOnce(ldapUrl, ldapBaseDN string, repoUrls []string, sourceBranch, targetBranch string, requiredApprovers int, dryRun bool, groupsAndUsers []string) error {
	baseUrl, repositories, err := parseRepositoryUrls(repoUrls)
	if err != nil {
		return err
	}

	bitbucketUser := os.Getenv("BITBUCKET_USERNAME")
	bitbucketToken := os.Getenv("BITBUCKET_TOKEN")
	if bitbucketUser == "" || bitbucketToken == "" {
		return fmt.Errorf("BITBUCKET_USERNAME and BITBUCKET_TOKEN must both be set")
	}
	bindDN := os.Getenv("LDAP_BIND_DN")
	ldapPassword := os.Getenv("LDAP_PASSWORD")
	if (bindDN == "") != (ldapPassword == "") {
		return fmt.Errorf("LDAP_BIND_DN and LDAP_PASSWORD must either both be set or both be omitted")
	}

	users, err := resolveGroupsAndUsers(ldapUrl, ldapBaseDN, bindDN, ldapPassword, groupsAndUsers)
	if err != nil {
		return err
	}
	if len(users) == 0 {
		return fmt.Errorf("the supplied groups contain no resolvable user accounts")
	}

	fmt.Printf("Got %d users from AD.\n", len(users))

	creds := bitbucketCredentials{
		Username: bitbucketUser,
		Token:    bitbucketToken,
	}

	return configureRepositoryTargets(repositories, func(repository repositoryTarget) error {
		if err := configureDefaultReviewers(baseUrl, repository.projectKey, repository.repoSlug, sourceBranch, targetBranch, requiredApprovers, users, creds, dryRun, os.Stdout); err != nil {
			return err
		}
		if dryRun {
			fmt.Printf("Dry run complete for %s/%s; no changes made.\n", repository.projectKey, repository.repoSlug)
		} else {
			fmt.Printf("Configured default reviewer(s) for %s/%s.\n", repository.projectKey, repository.repoSlug)
		}
		return nil
	})
}

type repositoryTarget struct {
	projectKey string
	repoSlug   string
}

func parseRepositoryUrls(repoUrls []string) (string, []repositoryTarget, error) {
	var baseUrl string
	repositories := make([]repositoryTarget, 0, len(repoUrls))
	for _, repoUrl := range repoUrls {
		projectKey, repoSlug, repositoryBaseUrl, err := parseRepositoryUrl(strings.TrimSpace(repoUrl))
		if err != nil {
			return "", nil, err
		}
		if baseUrl == "" {
			baseUrl = repositoryBaseUrl
		} else if repositoryBaseUrl != baseUrl {
			return "", nil, fmt.Errorf("all repository urls must use the same Bitbucket server; %q has a different base url", repoUrl)
		}
		repositories = append(repositories, repositoryTarget{projectKey: projectKey, repoSlug: repoSlug})
	}
	if len(repositories) == 0 {
		return "", nil, fmt.Errorf("at least one repository url is required")
	}
	return baseUrl, repositories, nil
}

func configureRepositoryTargets(repositories []repositoryTarget, configure func(repositoryTarget) error) error {
	var syncErrors []error
	for _, repository := range repositories {
		if err := configure(repository); err != nil {
			syncErrors = append(syncErrors, fmt.Errorf("%s/%s: %w", repository.projectKey, repository.repoSlug, err))
		}
	}
	return errors.Join(syncErrors...)
}

func runLoop(sleepSeconds int, fn func() error) error {
	if sleepSeconds <= 0 {
		return fn()
	}

	for {
		if err := fn(); err != nil {
			if errors.Is(err, errStopLoop) {
				return nil
			}
			log.Printf("sync failed: %v", err)
		}
		log.Printf("Sleeping for %d seconds before next sync...", sleepSeconds)
		time.Sleep(time.Duration(sleepSeconds) * time.Second)
	}
}
