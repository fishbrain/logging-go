package logging

import (
	"context"
	"errors"
	"fmt"
	stdlog "log"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/getsentry/sentry-go"
	nsq "github.com/nsqio/go-nsq"
	"github.com/sirupsen/logrus"
)

var (
	nsqDebugLevel = nsq.LogLevelDebug.String()
	nsqInfoLevel  = nsq.LogLevelInfo.String()
	nsqWarnLevel  = nsq.LogLevelWarning.String()
	nsqErrLevel   = nsq.LogLevelError.String()
	Log           *Logger
)

type LoggingConfig struct {
	LogLevel                 string
	Environment              string
	AppVersion               string
	ErrorNotifyReleaseStages []string
	SentryDSN                string
}

type Logger struct {
	*logrus.Logger
}

type sentryHook struct{}

type errorWithStacktrace struct {
	err        error
	stacktrace *sentry.Stacktrace
}

func (e *errorWithStacktrace) Error() string {
	return e.err.Error()
}

func (e *errorWithStacktrace) Unwrap() error {
	return e.err
}

func (l Logger) getNSQLogLevel() nsq.LogLevel {
	switch l.Level {
	case logrus.DebugLevel:
		return nsq.LogLevelDebug
	case logrus.InfoLevel:
		return nsq.LogLevelInfo
	case logrus.WarnLevel:
		return nsq.LogLevelWarning
	case logrus.ErrorLevel:
		return nsq.LogLevelError
	case logrus.FatalLevel:
		return nsq.LogLevelError
	case logrus.PanicLevel:
		return nsq.LogLevelError
	}

	return nsq.LogLevelInfo
}

type Entry struct {
	*logrus.Entry
}

// NSQLogger is an adaptor between go-nsq Logger and our
// standard logrus logger.
type NSQLogger struct{ *Entry }

// NewNSQLogrusLogger returns a new NSQLogger with the provided log level mapped
// to nsq.LogLevel for easily plugging into nsq.SetLogger.
func (l *Logger) NSQLogger() (NSQLogger, nsq.LogLevel) {
	return NSQLogger{l.NewEntry().WithField("component", "nsq")}, l.getNSQLogLevel()
}

// Output implements stdlib log.Logger.Output using logrus
// Decodes the go-nsq log messages to figure out the log level
func (n NSQLogger) Output(_ int, s string) error {
	if len(s) > 3 {
		msg := strings.TrimSpace(s[3:])
		switch s[:3] {
		case nsqDebugLevel:
			n.Debugln(msg)
		case nsqInfoLevel:
			n.Infoln(msg)
		case nsqWarnLevel:
			n.Warnln(msg)
		case nsqErrLevel:
			n.Errorln(msg)
		default:
			n.Infoln(msg)
		}
	}
	return nil
}

func (l *Logger) WithField(field string, value interface{}) *Entry {
	return l.NewEntry().WithField(field, value)
}

func (l *Logger) NewEntry() *Entry {
	return &Entry{logrus.NewEntry(l.Logger)}
}

func (l *Logger) WithDDTrace(ctx context.Context) *Entry {
	return l.NewEntry().WithDDTrace(ctx)
}

func (l *Logger) WithError(err error) *Entry {
	return l.NewEntry().WithError(err)
}

func (e *Entry) WithField(field string, value interface{}) *Entry {
	return &Entry{e.Entry.WithField(field, value)}
}

func (e *Entry) WithRutilus() *Entry {
	return &Entry{e.Entry.WithField("service", "rutilus")}
}

func (e *Entry) WithHTTPMethod(method string) *Entry {
	return &Entry{e.Entry.WithField("http.method", method)}
}

func (e *Entry) WithHTTPResponseCode(code int) *Entry {
	return &Entry{e.Entry.WithField("http.status_code", strconv.Itoa(code))}
}

// WithStringFieldIgnoreEmpty adds string value is empty - otherwise noop
func (e *Entry) WithStringFieldIgnoreEmpty(field string, value string) *Entry {
	if len(strings.TrimSpace(value)) > 0 {
		return e.WithField(field, value)
	}
	return e
}

func (e *Entry) WithUser(userID uint64) *Entry {
	return e.WithField("usr.id", userID)
}

// WithEvent parses and event given as string and returns an entry
// with event name, objectID and subjectID. If given event parses
// into more less than 2 or more than 3 parts, the full event string is
// returned as is.
func (e *Entry) WithEvent(event string) *Entry {
	split := strings.Split(event, ",")
	var objectID, subjectID int

	eventName := split[0]
	if len(split) == 2 {
		objectID, _ = strconv.Atoi(split[1])
		return e.
			WithStringFieldIgnoreEmpty("event_name", eventName).
			WithField("object_id", objectID)
	} else if len(split) == 3 {
		objectID, _ = strconv.Atoi(split[1])
		subjectID, _ = strconv.Atoi(split[2])
		return e.
			WithStringFieldIgnoreEmpty("event_name", eventName).
			WithField("object_id", objectID).
			WithField("subject_id", subjectID)
	}
	return e.WithStringFieldIgnoreEmpty("event", event)
}

func (e *Entry) WithRelation(relation string) *Entry {
	return e.WithStringFieldIgnoreEmpty("relation", relation)
}

func (e *Entry) WithNSQMessageID(id nsq.MessageID) *Entry {
	return e.WithStringFieldIgnoreEmpty("nsq_message_id", fmt.Sprintf("%s", id))
}

func (e *Entry) WithDuration(d time.Duration) *Entry {
	return e.
		WithField("duration", d.Nanoseconds())
}

func (e *Entry) WithE2EDuration(d time.Duration) *Entry {
	return e.WithField(
		"e2e_duration",
		d.Nanoseconds(),
	)
}

func (e *Entry) WithFCM() *Entry {
	return e.WithChannel("fcm")
}

func (e *Entry) WithNotificationlist() *Entry {
	return e.WithChannel("notificationlist")
}

func (e *Entry) WithChannel(channel string) *Entry {
	return e.WithField("channel", channel)
}

func (e *Entry) WithError(err error) *Entry {
	return &Entry{e.Entry.WithError(err)}
}

func (e *Entry) WithDDTrace(ctx context.Context) *Entry {
	var traceID, spanID uint64
	span, ok := tracer.SpanFromContext(ctx)
	if ok {
		traceID, spanID = span.Context().TraceIDLower(), span.Context().SpanID()
		return &Entry{e.Entry.WithFields(logrus.Fields{
			"dd.trace_id": traceID,
			"dd.span_id":  spanID,
		})}
	}
	return e
}

func ErrorWithStacktrace(err error) error {
	st := sentry.NewStacktrace()
	if st != nil && len(st.Frames) > 0 {
		st.Frames = st.Frames[:len(st.Frames)-1]
	}
	return &errorWithStacktrace{
		err:        err,
		stacktrace: st,
	}
}

func Errorf(format string, a ...interface{}) error {
	st := sentry.NewStacktrace()
	if st != nil && len(st.Frames) > 0 {
		st.Frames = st.Frames[:len(st.Frames)-1]
	}
	return &errorWithStacktrace{
		err:        fmt.Errorf(format, a...),
		stacktrace: st,
	}
}

func getLogrusLogLevel(level string) logrus.Level {
	lookup := map[string]logrus.Level{
		"ERROR":   logrus.ErrorLevel,
		"WARNING": logrus.WarnLevel,
		"INFO":    logrus.InfoLevel,
		"DEBUG":   logrus.DebugLevel,
	}

	loglevel, ok := lookup[level]

	if !ok {
		loglevel = logrus.InfoLevel
	}

	return loglevel
}

func (s *sentryHook) Fire(entry *logrus.Entry) error {
	var notifyErr error
	var origErr error
	switch err := entry.Data[logrus.ErrorKey].(type) {
	case error:
		origErr = err
		if entry.Message != "" {
			notifyErr = fmt.Errorf("%s: %w", entry.Message, err)
		} else {
			notifyErr = err
		}
	default:
		notifyErr = fmt.Errorf("%s", entry.Message)
		origErr = notifyErr
	}

	event := sentry.NewEvent()
	event.Level = sentry.LevelError
	if entry.Level == logrus.FatalLevel {
		event.Level = sentry.LevelFatal
	}
	event.Message = notifyErr.Error()
	var stacktrace *sentry.Stacktrace
	var errSt *errorWithStacktrace
	if errors.As(notifyErr, &errSt) {
		stacktrace = errSt.stacktrace
	}

	event.Exception = []sentry.Exception{{
		Type:       errorClass(origErr),
		Value:      notifyErr.Error(),
		Stacktrace: stacktrace,
	}}

	extra := make(sentry.Context)
	for key, val := range entry.Data {
		if key != logrus.ErrorKey {
			extra[key] = val
		}
	}
	if len(extra) > 0 {
		event.Contexts["extra"] = extra
	}

	sentry.CaptureEvent(event)

	if entry.Level == logrus.FatalLevel || entry.Level == logrus.PanicLevel {
		sentry.Flush(2 * time.Second)
	}
	return nil
}

// errorClass returns a stable type name for Sentry grouping by unwrapping
// transparent wrappers (fmt.Errorf with %w, errorWithStacktrace) up to a
// bounded depth so events group on the underlying error type rather than the
// wrapper.
func errorClass(err error) string {
	for i := 0; i < 11; i++ {
		t := reflect.TypeOf(err).String()
		if t != "*fmt.wrapError" && t != "*logging.errorWithStacktrace" {
			return t
		}
		unwrapped := errors.Unwrap(err)
		if unwrapped == nil {
			return t
		}
		err = unwrapped
	}
	return reflect.TypeOf(err).String()
}

// Flush blocks up to timeout waiting for queued Sentry events to be delivered.
// Call at service shutdown so the last events aren't lost when the process exits.
func Flush(timeout time.Duration) bool {
	return sentry.Flush(timeout)
}

func (s *sentryHook) Levels() []logrus.Level {
	return []logrus.Level{
		logrus.ErrorLevel,
		logrus.FatalLevel,
		logrus.PanicLevel,
	}
}

func new(withSentry bool, config LoggingConfig) *Logger {
	log := logrus.New()
	logrus.ErrorKey = "error.message"
	log.Formatter = &logrus.JSONFormatter{
		TimestampFormat: time.RFC3339Nano,
		FieldMap: logrus.FieldMap{
			logrus.FieldKeyMsg:  "message",
			logrus.FieldKeyFunc: "logger.method_name",
			logrus.FieldKeyFile: "logger.name",
		},
	}
	log.Level = getLogrusLogLevel(config.LogLevel)

	if withSentry {
		log.Hooks.Add(&sentryHook{})
	}

	return &Logger{log}
}

func shouldNotify(releaseStages []string, environment string) bool {
	for _, stage := range releaseStages {
		if stage == environment {
			return true
		}
	}
	return false
}

func Init(config LoggingConfig) {
	if nil == Log {
		withSentry := false
		if config.SentryDSN != "" && shouldNotify(config.ErrorNotifyReleaseStages, config.Environment) {
			err := sentry.Init(sentry.ClientOptions{
				Dsn:         config.SentryDSN,
				Environment: config.Environment,
				Release:     config.AppVersion,
			})
			if err != nil {
				stdlog.Printf("sentry.Init: %s", err)
			} else {
				withSentry = true
			}
		}

		Log = new(withSentry, config)
	}
}
