package osix

import (
	"fmt"
	"strconv"
	"strings"
)

type SideEffectBlockedError struct {
	Decision SideEffectDecision
}

func (e SideEffectBlockedError) Error() string {
	return fmt.Sprintf("side-effect action %s: %s", e.Decision.Action, e.Decision.Reason)
}

type GitHubIssueAdapter struct {
	Repo string
}

type GmailAdapter struct {
	Mailbox string
}

type GoogleCalendarAdapter struct {
	CalendarID string
}

type LinearAdapter struct {
	Workspace string
}

type SlackAdapter struct {
	Workspace string
}

func NewGitHubIssueAdapter(repo string) (GitHubIssueAdapter, error) {
	repo = strings.Trim(strings.TrimSpace(repo), "/")
	if repo == "" || !strings.Contains(repo, "/") {
		return GitHubIssueAdapter{}, fmt.Errorf("GitHub repo must be OWNER/REPO")
	}
	return GitHubIssueAdapter{Repo: repo}, nil
}

func NewGmailAdapter(mailbox string) (GmailAdapter, error) {
	mailbox = strings.Trim(strings.TrimSpace(mailbox), "/")
	if mailbox == "" {
		mailbox = "default"
	}
	if strings.ContainsAny(mailbox, " \t\r\n") {
		return GmailAdapter{}, fmt.Errorf("Gmail mailbox must not contain whitespace")
	}
	return GmailAdapter{Mailbox: mailbox}, nil
}

func NewGoogleCalendarAdapter(calendarID string) (GoogleCalendarAdapter, error) {
	calendarID = strings.Trim(strings.TrimSpace(calendarID), "/")
	if calendarID == "" {
		calendarID = "primary"
	}
	if strings.ContainsAny(calendarID, " \t\r\n") {
		return GoogleCalendarAdapter{}, fmt.Errorf("Google Calendar ID must not contain whitespace")
	}
	return GoogleCalendarAdapter{CalendarID: calendarID}, nil
}

func NewLinearAdapter(workspace string) (LinearAdapter, error) {
	workspace = strings.Trim(strings.TrimSpace(workspace), "/")
	if workspace == "" {
		workspace = "default"
	}
	if strings.ContainsAny(workspace, " \t\r\n") {
		return LinearAdapter{}, fmt.Errorf("Linear workspace must not contain whitespace")
	}
	return LinearAdapter{Workspace: workspace}, nil
}

func NewSlackAdapter(workspace string) (SlackAdapter, error) {
	workspace = strings.Trim(strings.TrimSpace(workspace), "/")
	if workspace == "" {
		workspace = "default"
	}
	if strings.ContainsAny(workspace, " \t\r\n") {
		return SlackAdapter{}, fmt.Errorf("Slack workspace must not contain whitespace")
	}
	return SlackAdapter{Workspace: workspace}, nil
}

func (a GitHubIssueAdapter) CheckReadIssue(target string, number int64) (SideEffectDecision, error) {
	if number <= 0 {
		return SideEffectDecision{}, fmt.Errorf("GitHub issue number must be positive")
	}
	return CheckSideEffect(target, SideEffectCheck{
		Tool:             "github.issue.read",
		ExternalResource: a.issueResource(number),
		Operation:        SideEffectOperationRead,
	})
}

func (a GitHubIssueAdapter) CheckCreateIssue(target, idempotencyKey string) (SideEffectDecision, error) {
	return a.checkWrite(target, "github.issue.create", a.issuesResource(), idempotencyKey)
}

func (a GitHubIssueAdapter) CheckCommentIssue(target string, number int64, idempotencyKey string) (SideEffectDecision, error) {
	if number <= 0 {
		return SideEffectDecision{}, fmt.Errorf("GitHub issue number must be positive")
	}
	return a.checkWrite(target, "github.issue.comment", a.issueResource(number)+"/comments", idempotencyKey)
}

func (a GitHubIssueAdapter) checkWrite(target, tool, resource, idempotencyKey string) (SideEffectDecision, error) {
	return checkProviderWrite(target, tool, resource, idempotencyKey)
}

func (a GitHubIssueAdapter) issuesResource() string {
	return "github:" + a.Repo + "/issues"
}

func (a GitHubIssueAdapter) issueResource(number int64) string {
	return a.issuesResource() + "/" + strconv.FormatInt(number, 10)
}

func (a GmailAdapter) CheckReadMessage(target, messageID string) (SideEffectDecision, error) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return SideEffectDecision{}, fmt.Errorf("Gmail message ID is required")
	}
	return CheckSideEffect(target, SideEffectCheck{
		Tool:             "gmail.message.read",
		ExternalResource: a.messageResource(messageID),
		Operation:        SideEffectOperationRead,
	})
}

func (a GmailAdapter) CheckReadThread(target, threadID string) (SideEffectDecision, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return SideEffectDecision{}, fmt.Errorf("Gmail thread ID is required")
	}
	return CheckSideEffect(target, SideEffectCheck{
		Tool:             "gmail.thread.read",
		ExternalResource: a.threadResource(threadID),
		Operation:        SideEffectOperationRead,
	})
}

func (a GmailAdapter) CheckSendMessage(target, idempotencyKey string) (SideEffectDecision, error) {
	return a.checkWrite(target, "gmail.message.send", a.sendResource(), idempotencyKey)
}

func (a GmailAdapter) CheckCreateDraft(target, idempotencyKey string) (SideEffectDecision, error) {
	return a.checkWrite(target, "gmail.draft.create", a.draftsResource(), idempotencyKey)
}

func (a GmailAdapter) CheckModifyMessageLabels(target, messageID, idempotencyKey string) (SideEffectDecision, error) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return SideEffectDecision{}, fmt.Errorf("Gmail message ID is required")
	}
	return a.checkWrite(target, "gmail.message.modify_labels", a.messageResource(messageID)+"/labels", idempotencyKey)
}

func (a GmailAdapter) checkWrite(target, tool, resource, idempotencyKey string) (SideEffectDecision, error) {
	return checkProviderWrite(target, tool, resource, idempotencyKey)
}

func (a GmailAdapter) mailboxResource() string {
	return "gmail:" + a.Mailbox
}

func (a GmailAdapter) messageResource(messageID string) string {
	return a.mailboxResource() + "/messages/" + messageID
}

func (a GmailAdapter) threadResource(threadID string) string {
	return a.mailboxResource() + "/threads/" + threadID
}

func (a GmailAdapter) sendResource() string {
	return a.mailboxResource() + "/send"
}

func (a GmailAdapter) draftsResource() string {
	return a.mailboxResource() + "/drafts"
}

func (a GoogleCalendarAdapter) CheckReadEvent(target, eventID string) (SideEffectDecision, error) {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return SideEffectDecision{}, fmt.Errorf("Google Calendar event ID is required")
	}
	return CheckSideEffect(target, SideEffectCheck{
		Tool:             "google_calendar.event.read",
		ExternalResource: a.eventResource(eventID),
		Operation:        SideEffectOperationRead,
	})
}

func (a GoogleCalendarAdapter) CheckCreateEvent(target, idempotencyKey string) (SideEffectDecision, error) {
	return a.checkWrite(target, "google_calendar.event.create", a.eventsResource(), idempotencyKey)
}

func (a GoogleCalendarAdapter) CheckUpdateEvent(target, eventID, idempotencyKey string) (SideEffectDecision, error) {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return SideEffectDecision{}, fmt.Errorf("Google Calendar event ID is required")
	}
	return a.checkWrite(target, "google_calendar.event.update", a.eventResource(eventID), idempotencyKey)
}

func (a GoogleCalendarAdapter) CheckDeleteEvent(target, eventID, idempotencyKey string) (SideEffectDecision, error) {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return SideEffectDecision{}, fmt.Errorf("Google Calendar event ID is required")
	}
	return a.checkWrite(target, "google_calendar.event.delete", a.eventResource(eventID), idempotencyKey)
}

func (a GoogleCalendarAdapter) CheckRespondInvitation(target, eventID, idempotencyKey string) (SideEffectDecision, error) {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return SideEffectDecision{}, fmt.Errorf("Google Calendar event ID is required")
	}
	return a.checkWrite(target, "google_calendar.event.respond", a.eventResource(eventID)+"/response", idempotencyKey)
}

func (a GoogleCalendarAdapter) checkWrite(target, tool, resource, idempotencyKey string) (SideEffectDecision, error) {
	return checkProviderWrite(target, tool, resource, idempotencyKey)
}

func checkProviderWrite(target, tool, resource, idempotencyKey string) (SideEffectDecision, error) {
	decision, err := CheckSideEffect(target, SideEffectCheck{
		Tool:             tool,
		ExternalResource: resource,
		Operation:        SideEffectOperationWrite,
		IdempotencyKey:   idempotencyKey,
	})
	if err != nil {
		return SideEffectDecision{}, err
	}
	if decision.Action == SideEffectActionAllow || decision.Action == SideEffectActionMock {
		return decision, nil
	}
	return decision, SideEffectBlockedError{Decision: decision}
}

func (a GoogleCalendarAdapter) calendarResource() string {
	return "gcal:" + a.CalendarID
}

func (a GoogleCalendarAdapter) eventsResource() string {
	return a.calendarResource() + "/events"
}

func (a GoogleCalendarAdapter) eventResource(eventID string) string {
	return a.eventsResource() + "/" + eventID
}

func (a LinearAdapter) CheckReadIssue(target, issueID string) (SideEffectDecision, error) {
	issueID = strings.TrimSpace(issueID)
	if issueID == "" {
		return SideEffectDecision{}, fmt.Errorf("Linear issue ID is required")
	}
	return CheckSideEffect(target, SideEffectCheck{
		Tool:             "linear.issue.read",
		ExternalResource: a.issueResource(issueID),
		Operation:        SideEffectOperationRead,
	})
}

func (a LinearAdapter) CheckCreateIssue(target, teamKey, idempotencyKey string) (SideEffectDecision, error) {
	teamKey = strings.Trim(strings.TrimSpace(teamKey), "/")
	if teamKey == "" {
		return SideEffectDecision{}, fmt.Errorf("Linear team key is required")
	}
	return checkProviderWrite(target, "linear.issue.create", a.teamIssuesResource(teamKey), idempotencyKey)
}

func (a LinearAdapter) CheckUpdateIssue(target, issueID, idempotencyKey string) (SideEffectDecision, error) {
	issueID = strings.TrimSpace(issueID)
	if issueID == "" {
		return SideEffectDecision{}, fmt.Errorf("Linear issue ID is required")
	}
	return checkProviderWrite(target, "linear.issue.update", a.issueResource(issueID), idempotencyKey)
}

func (a LinearAdapter) CheckCommentIssue(target, issueID, idempotencyKey string) (SideEffectDecision, error) {
	issueID = strings.TrimSpace(issueID)
	if issueID == "" {
		return SideEffectDecision{}, fmt.Errorf("Linear issue ID is required")
	}
	return checkProviderWrite(target, "linear.comment.create", a.issueResource(issueID)+"/comments", idempotencyKey)
}

func (a LinearAdapter) workspaceResource() string {
	return "linear:" + a.Workspace
}

func (a LinearAdapter) teamIssuesResource(teamKey string) string {
	return a.workspaceResource() + "/teams/" + teamKey + "/issues"
}

func (a LinearAdapter) issueResource(issueID string) string {
	return a.workspaceResource() + "/issues/" + issueID
}

func (a SlackAdapter) CheckReadChannel(target, channelID string) (SideEffectDecision, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return SideEffectDecision{}, fmt.Errorf("Slack channel ID is required")
	}
	return CheckSideEffect(target, SideEffectCheck{
		Tool:             "slack.channel.read",
		ExternalResource: a.channelResource(channelID),
		Operation:        SideEffectOperationRead,
	})
}

func (a SlackAdapter) CheckPostMessage(target, channelID, idempotencyKey string) (SideEffectDecision, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return SideEffectDecision{}, fmt.Errorf("Slack channel ID is required")
	}
	return checkProviderWrite(target, "slack.message.post", a.channelResource(channelID)+"/messages", idempotencyKey)
}

func (a SlackAdapter) CheckUpdateMessage(target, channelID, messageID, idempotencyKey string) (SideEffectDecision, error) {
	resource, err := a.messageResource(channelID, messageID)
	if err != nil {
		return SideEffectDecision{}, err
	}
	return checkProviderWrite(target, "slack.message.update", resource, idempotencyKey)
}

func (a SlackAdapter) CheckDeleteMessage(target, channelID, messageID, idempotencyKey string) (SideEffectDecision, error) {
	resource, err := a.messageResource(channelID, messageID)
	if err != nil {
		return SideEffectDecision{}, err
	}
	return checkProviderWrite(target, "slack.message.delete", resource, idempotencyKey)
}

func (a SlackAdapter) CheckAddReaction(target, channelID, messageID, reaction, idempotencyKey string) (SideEffectDecision, error) {
	resource, err := a.messageResource(channelID, messageID)
	if err != nil {
		return SideEffectDecision{}, err
	}
	reaction = strings.Trim(strings.TrimSpace(reaction), ":/")
	if reaction == "" {
		return SideEffectDecision{}, fmt.Errorf("Slack reaction is required")
	}
	return checkProviderWrite(target, "slack.reaction.add", resource+"/reactions/"+reaction, idempotencyKey)
}

func (a SlackAdapter) workspaceResource() string {
	return "slack:" + a.Workspace
}

func (a SlackAdapter) channelResource(channelID string) string {
	return a.workspaceResource() + "/channels/" + channelID
}

func (a SlackAdapter) messageResource(channelID, messageID string) (string, error) {
	channelID = strings.TrimSpace(channelID)
	messageID = strings.TrimSpace(messageID)
	if channelID == "" {
		return "", fmt.Errorf("Slack channel ID is required")
	}
	if messageID == "" {
		return "", fmt.Errorf("Slack message ID is required")
	}
	return a.channelResource(channelID) + "/messages/" + messageID, nil
}
