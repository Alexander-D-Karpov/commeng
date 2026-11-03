package domain

import (
	"errors"
	"fmt"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Role string

const (
	RoleUser      Role = "USER"
	RoleModerator Role = "MODERATOR"
	RoleAdmin     Role = "ADMIN"
)

type State string

const (
	StateVisible State = "VISIBLE"
	StateHidden  State = "HIDDEN"
	StateDeleted State = "DELETED"
)

type Sort string

const (
	SortNewest Sort = "NEWEST"
	SortOldest Sort = "OLDEST"
)

type Code string

const (
	ErrNotFound       Code = "NOT_FOUND"
	ErrInput          Code = "BAD_USER_INPUT"
	ErrAuth           Code = "UNAUTHENTICATED"
	ErrForbidden      Code = "FORBIDDEN"
	ErrDisabled       Code = "COMMENTS_DISABLED"
	ErrConflict       Code = "VERSION_CONFLICT"
	ErrAlreadyExists  Code = "ALREADY_EXISTS"
	ErrCursor         Code = "INVALID_CURSOR"
	ErrResource       Code = "RESOURCE_EXHAUSTED"
	ErrHistoryExpired Code = "EVENT_HISTORY_EXPIRED"
)

type Error struct {
	Code    Code
	Message string
}

func (e *Error) Error() string {
	return e.Message
}
func Fault(code Code, message string) error {
	return &Error{Code: code, Message: message}
}

// Field qualifies a validation failure with its input field without losing its code.
func Field(name string, err error) error {
	var inputError *Error
	if errors.As(err, &inputError) {
		return &Error{Code: inputError.Code, Message: name + ": " + inputError.Message}
	}
	return err
}

func HasCode(err error, code Code) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

type User struct {
	ID           int64
	Name         string
	Email        string
	PasswordHash string
	Role         Role
}
type Post struct {
	ID              int64
	AuthorID        int64
	Title           string
	Body            string
	CommentsEnabled bool
	CommentCount    int64
	TreeVersion     int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
	Deleted         bool
	EventVersion    int64
}
type Comment struct {
	ID            int64
	PostID        int64
	ParentID      *int64
	Name          string
	Email         string
	AvatarHash    string
	UserID        *int64
	IsPostAuthor  bool
	ThreadBlocked bool
	Body          string
	State         State
	Locked        bool
	ReplyCount    int64
	LikesCount    int64
	CreatedAt     time.Time
	EditedAt      *time.Time
}
type CommentCreate struct {
	UserID     *int64
	PostID     int64
	ParentID   *int64
	Name       string
	Email      string
	AvatarHash string
	Body       string
}
type CommentPageRequest struct {
	PostID   int64
	ParentID *int64
	After    *int64
	Before   *int64
	First    int
	Sort     Sort
}
type Page[T any] struct {
	Items   []T
	HasNext bool
	HasPrev bool
}

// AncestorPage is ordered nearest-parent-first for efficient continuation.
type AncestorPage struct {
	Items      []Comment
	NextBefore *int64
}

type LocatedComment struct {
	Target      Comment
	Page        Page[Comment]
	PageNumber  int64
	IndexInPage int
}

// CommentEvent is a durable position within one post. Its sequence is assigned
// under that post row lock, so concurrent writers cannot commit out of order.
type CommentEvent struct {
	Sequence  int64
	PostID    int64
	Comment   Comment
	CreatedAt time.Time
}

// Notification is only generated for replies to authenticated accounts;
// anonymous email addresses are not verified and are never used for delivery.
type Notification struct {
	ID          int64
	UserID      int64
	PostID      int64
	CommentID   int64
	ActorUserID *int64
	Kind        string
	CreatedAt   time.Time
	ReadAt      *time.Time
}

type ModerationKind string

const (
	HideComment    ModerationKind = "HIDE"
	RestoreComment ModerationKind = "RESTORE"
	DeleteComment  ModerationKind = "DELETE"
	MoveComment    ModerationKind = "MOVE"
	LockComment    ModerationKind = "LOCK"
	UnlockComment  ModerationKind = "UNLOCK"
)

type Moderation struct {
	CommentID       int64
	ActorID         int64
	Kind            ModerationKind
	NewParentID     *int64
	ExpectedVersion int64
	Reason          string
}

func Text(raw string, max int) (string, error) {
	value := strings.TrimSpace(raw)
	length := utf8.RuneCountInString(value)
	if !utf8.ValidString(value) || length == 0 || length > max ||
		strings.IndexFunc(value, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) >= 0 {
		return "", Fault(ErrInput, fmt.Sprintf("text must contain 1–%d Unicode code points", max))
	}
	return value, nil
}

// LabelText rejects control characters in single-line labels such as names and titles.
// Comment and post bodies use Text and may contain normal line breaks and tabs.
func LabelText(raw string, max int) (string, error) {
	value, err := Text(raw, max)
	if err != nil {
		return "", err
	}
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", Fault(ErrInput, "control characters are not allowed")
	}
	return value, nil
}

func ParseID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != value {
		return 0, Fault(ErrInput, "invalid ID")
	}
	return id, nil
}
func ID(id int64) string {
	return strconv.FormatInt(id, 10)
}
func NormalizeEmail(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if len(value) > 254 || strings.ContainsAny(value, "\r\n\x00") || !utf8.ValidString(value) {
		return "", Fault(ErrInput, "invalid email")
	}
	addr, err := mail.ParseAddress(value)
	if err != nil || addr.Address != value || !strings.Contains(value, "@") {
		return "", Fault(ErrInput, "invalid email")
	}
	return value, nil
}
func ValidatePageSize(n int) error {
	if n < 1 || n > 100 {
		return Fault(ErrInput, "page size must be 1–100")
	}
	return nil
}
func ValidSort(s Sort) bool {
	return s == SortNewest || s == SortOldest
}
