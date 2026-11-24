package app

import (
	"context"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"mise/api/internal/tasks/domain"
)

type Environment struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

var githubRepository = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*/[a-zA-Z0-9_.-]+$`)

type ProjectSettings struct {
	GitHub         *PlanPolicy
	RequiredChecks *[]string
	AutoDispatch   *bool
	DispatchLimit  *int
	AutoMerge      *bool
}

func (p ProjectSettings) Empty() bool {
	return p.GitHub == nil && p.RequiredChecks == nil && p.AutoDispatch == nil && p.DispatchLimit == nil && p.AutoMerge == nil
}

func (s *Service) UpdateProjectSettings(
	ctx context.Context,
	actor domain.Actor,
	key string,
	settings ProjectSettings,
) error {
	if actor.Kind != domain.Human {
		return &domain.RuleError{Code: domain.CodeForbidden, Msg: "only a human may change a project"}
	}
	if settings.Empty() {
		return invalidInput("no project setting to change")
	}
	if p := settings.GitHub; p != nil {
		if !githubRepository.MatchString(p.Repository) || strings.HasSuffix(p.Repository, "/.") || strings.HasSuffix(p.Repository, "/..") || p.WorkflowID <= 0 || p.Branch == "" || strings.ContainsAny(p.Branch, " ~^:?*[\\\t\r\n") || strings.Contains(p.Branch, "..") {
			return invalidInput("provide an owner/repository, branch name and positive workflow ID")
		}
	}
	if settings.RequiredChecks != nil {
		if err := s.UpdateProjectChecks(ctx, actor, key, *settings.RequiredChecks); err != nil {
			return err
		}
	}
	if settings.AutoDispatch != nil || settings.DispatchLimit != nil {
		current, err := s.repo.DispatchPolicy(ctx, key)
		if err != nil {
			return err
		}
		enabled, limit := current.Enabled, current.Limit
		if settings.AutoDispatch != nil {
			enabled = *settings.AutoDispatch
		}
		if settings.DispatchLimit != nil {
			limit = *settings.DispatchLimit
		}
		if err := s.UpdateProjectDispatch(ctx, actor, key, enabled, limit); err != nil {
			return err
		}
	}
	if settings.GitHub != nil {
		return s.repo.SetProjectGitHub(ctx, key, *settings.GitHub, settings.AutoMerge)
	}
	if settings.AutoMerge != nil {
		if err := s.repo.SetProjectAutoMerge(ctx, key, *settings.AutoMerge); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) Environments(ctx context.Context, actor domain.Actor, key string) ([]Environment, error) {
	if _, err := readScope(actor); err != nil {
		return nil, err
	}
	if actor.Kind == domain.Viewer {
		return nil, &domain.RuleError{Code: domain.CodeForbidden, Msg: "a credential is required to inspect environments"}
	}
	return s.repo.ListEnvironments(ctx, key)
}

func (s *Service) AddEnvironment(
	ctx context.Context,
	actor domain.Actor,
	key string,
	name string,
	rawURL string,
) (Environment, error) {
	if actor.Kind != domain.Human {
		return Environment{}, &domain.RuleError{Code: domain.CodeForbidden, Msg: "only a human may register an environment"}
	}
	cleanName := strings.TrimSpace(name)
	if cleanName == "" || utf8.RuneCountInString(cleanName) > 40 {
		return Environment{}, invalidInput("environment name must be between 1 and 40 characters")
	}
	cleanURL := strings.TrimSpace(rawURL)
	if utf8.RuneCountInString(cleanURL) > 500 {
		return Environment{}, invalidInput("environment url must be at most 500 characters")
	}
	parsed, err := url.Parse(cleanURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return Environment{}, invalidInput("environment url must be an absolute http(s) URL")
	}
	environment := Environment{Name: cleanName, URL: strings.TrimRight(cleanURL, "/")}
	if err := s.repo.AddEnvironment(ctx, key, environment); err != nil {
		return Environment{}, err
	}
	return environment, nil
}
