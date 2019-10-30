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

	"github.com/bugsnag/bugsnag-go"
	bugsnag_errors "github.com/bugsnag/bugsnag-go/errors"
	nsq "github.com/nsqio/go-nsq"
	"github.com/sirupsen/logrus"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

var (
	nsqDebugLevel = nsq.LogLevelDebug.String()
	nsqInfoLevel  = nsq.LogLevelInfo.String()
	nsqWarnLevel  = nsq.LogLevelWarning.String()
	nsqErrLevel   = nsq.LogLevelError.String()
	Log           *Logger
)

type LoggingConfig struct {
	LogLevel                   string
	Environment                string
	AppVersion                 string
	BugsnagAPIKey              string
	BugsnagNotifyReleaseStages []string
	BugsnagProjectPackages     []string
	BugsnagProjectPaths        []string
	BugsnagPackageRoot         string
}

type Logger struct {
	*logrus.Logger
}

type bugsnagHook struct{}

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
	return &Entry{e.Entry.WithField("http_method", method)}
}

func (e *Entry) WithHTTPResponseCode(code int) *Entry {
	return &Entry{e.Entry.WithField("http_response_code", strconv.Itoa(code))}
}

// WithStringFieldIgnoreEmpty adds string value is empty - otherwise noop
func (e *Entry) WithStringFieldIgnoreEmpty(field string, value string) *Entry {
	if len(strings.TrimSpace(value)) > 0 {
		return e.WithField(field, value)
	}
	return e
}

func (e *Entry) WithUser(userID uint64) *Entry {
	return e.WithField("user_id", userID)
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
		WithField("duration_ms", d.Round(time.Millisecond).Nanoseconds()/1000000)
}

func (e *Entry) WithE2EDuration(d time.Duration) *Entry {
	return e.WithField(
		"e2e_duration_ms",
		d.Round(time.Millisecond).Nanoseconds()/1000000,
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
		// there was a span in the context
		traceID, spanID = span.Context().TraceID(), span.Context().SpanID()
		return &Entry{e.Entry.WithFields(logrus.Fields{
			"dd.trace_id": traceID,
			"dd.span_id":  spanID,
		})}
	}
	return e
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

func (b *bugsnagHook) Fire(entry *logrus.Entry) error {
	var notifyErr error
	err, ok := entry.Data["error"].(error)
	if ok {
		if entry.Message != "" {
			notifyErr = fmt.Errorf("%s: %w", entry.Message, err)
		} else {
			notifyErr = err
		}
	} else {
		notifyErr = fmt.Errorf("%s", entry.Message)
	}

	metadata := bugsnag.MetaData{}
	metadata["metadata"] = make(map[string]interface{})
	for key, val := range entry.Data {
		if key != "error" {
			metadata["metadata"][key] = val
		}
	}

	skipStackFrames := 4
	errWithStack := bugsnag_errors.New(notifyErr, skipStackFrames)
	bugsnagErr := bugsnag.Notify(errWithStack, metadata)
	if bugsnagErr != nil {
		return bugsnagErr
	}

	return nil
}

func (b *bugsnagHook) Levels() []logrus.Level {
	return []logrus.Level{
		logrus.ErrorLevel,
		logrus.FatalLevel,
		logrus.PanicLevel,
	}
}

func new(withBugsnag bool, config LoggingConfig) *Logger {
	log := logrus.New()
	log.Formatter = &logrus.JSONFormatter{
		TimestampFormat: time.RFC3339Nano,
		FieldMap: logrus.FieldMap{
			logrus.FieldKeyMsg: "message",
		},
	}
	log.Level = getLogrusLogLevel(config.LogLevel)

	if withBugsnag {
		log.Hooks.Add(&bugsnagHook{})
	}

	return &Logger{log}
}

func Init(config LoggingConfig) {
	if nil == Log {
		bugsnag.Configure(bugsnag.Configuration{
			APIKey:              config.BugsnagAPIKey,
			ReleaseStage:        config.Environment,
			AppVersion:          config.AppVersion,
			NotifyReleaseStages: config.BugsnagNotifyReleaseStages,
			ProjectPackages:     config.BugsnagProjectPackages,
			ProjectPaths:        config.BugsnagProjectPaths,
			PackageRoot:         config.BugsnagPackageRoot,
			Logger:              stdlog.New(new(false, config).Writer(), "bugsnag: ", 0),
		})
		bugsnag.OnBeforeNotify(
			func(event *bugsnag.Event, config *bugsnag.Configuration) error {
				errClass := event.ErrorClass
				count := 0
				for {
					if errClass != "*fmt.wrapError" {
						break
					}

					wrappedError := errors.Unwrap(event.Error.Err)
					if wrappedError != nil {
						errClass = reflect.TypeOf(wrappedError).String()
					} else {
						break
					}
					count++
					if count >= 11 {
						Log.Infof("Failed to unwrap error %s %s %+v", event.ErrorClass, errClass, event.Error)
					}
				}
				event.ErrorClass = errClass
				return nil
			})
		Log = new(true, config)
	}
}
