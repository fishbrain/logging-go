package logging

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/getsentry/sentry-go"
	"github.com/nsqio/go-nsq"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

type mockLogFile struct {
	out *os.File
	in  *os.File
}

func newMockLogFile(t *testing.T) mockLogFile {
	r, w, err := os.Pipe()
	assert.NoError(t, err)

	return mockLogFile{r, w}
}

func (f mockLogFile) getLogFileContent(t *testing.T) string {
	err := f.in.Close()
	assert.NoError(t, err)
	out, err := ioutil.ReadAll(f.out)
	assert.NoError(t, err)

	return string(out)
}

var testGetLogrusLogLevelData = []struct {
	in  string
	out logrus.Level
}{
	{"", logrus.InfoLevel},
	{"ashtashtnn212rn2h1h12hxxz", logrus.InfoLevel},
	{"ERROR", logrus.ErrorLevel},
	{"WARNING", logrus.WarnLevel},
	{"INFO", logrus.InfoLevel},
	{"DEBUG", logrus.DebugLevel},
}

func TestMain(m *testing.M) {
	Init(LoggingConfig{})
	os.Exit(m.Run())
}

func TestGetLogrusLogLevel(t *testing.T) {
	for _, test := range testGetLogrusLogLevelData {
		if getLogrusLogLevel(test.in) != test.out {
			t.Errorf("failed to get default log level of [%s]", test.in)
		}
	}
}

func TestNew(t *testing.T) {
	logFile := newMockLogFile(t)

	Log.Out = logFile.in
	Log.Level = logrus.InfoLevel

	Log.Debug("test debug")
	Log.Info("test info")
	Log.Warning("test warning")
	Log.Error("test error")

	_log := logFile.getLogFileContent(t)

	if strings.Contains(_log, "test debug") {
		t.Error("log should not contain debug since the level is ignored")
	}
	if !strings.Contains(_log, "test info") {
		t.Error("failed to log info")
	}
	if !strings.Contains(_log, "test warning") {
		t.Error("failed to log warning")
	}
	if !strings.Contains(_log, "test error") {
		t.Error("failed to log error")
	}
}

func TestConcurrentUseOfEntry(t *testing.T) {
	logFile := newMockLogFile(t)
	Log.Logger.Out = logFile.in
	Log.Logger.Level = logrus.DebugLevel
	entry := Log.NewEntry()
	userEntry := entry.WithUser(10)

	wg := sync.WaitGroup{}
	wg.Add(5)
	go func() { defer wg.Done(); userEntry.WithChannel("asdf").Info("test1") }()
	go func() { defer wg.Done(); entry.WithChannel("asdgegege").Info("test2") }()
	go func() { defer wg.Done(); entry.WithChannel("asdgegege").Debug("test3") }()
	go func() { defer wg.Done(); entry.WithChannel("asdgegege").Error("test4") }()
	go func() { defer wg.Done(); Log.Info("test5") }()
	wg.Wait()

	logFileContent := logFile.getLogFileContent(t)

	assert.Contains(t, logFileContent, "test1")
	assert.Contains(t, logFileContent, "test2")
	assert.Contains(t, logFileContent, "test3")
	assert.Contains(t, logFileContent, "test4")
	assert.Contains(t, logFileContent, "test5")
}

func TestWithStringFieldIgnoreEmpty(t *testing.T) {
	Log.Logger.Level = logrus.DebugLevel

	t.Run("no field if string empty", func(t *testing.T) {
		logFile := newMockLogFile(t)
		Log.Logger.Out = logFile.in

		Log.NewEntry().WithStringFieldIgnoreEmpty("nsq", "").Info("crap")
		logFileContent := logFile.getLogFileContent(t)
		assert.NotContains(t, logFileContent, "nsq", "nsq should not be in the log since value is empty")
	})
	t.Run("field present if string is non empty", func(t *testing.T) {
		logFile := newMockLogFile(t)
		Log.Logger.Out = logFile.in

		Log.NewEntry().WithStringFieldIgnoreEmpty("nsq", "asdf").Info("crap")
		logFileContent := logFile.getLogFileContent(t)
		assert.Contains(t, logFileContent, "nsq", "nsq should be in the log since value is non empty")
	})
}

func TestSentryHookFire(t *testing.T) {
	hook := &sentryHook{}

	t.Run("fires on error entry with error", func(t *testing.T) {
		entry := &logrus.Entry{
			Level:   logrus.ErrorLevel,
			Message: "something went wrong",
			Data: logrus.Fields{
				logrus.ErrorKey: fmt.Errorf("test error"),
			},
		}
		err := hook.Fire(entry)
		assert.NoError(t, err)
	})

	t.Run("fires on error entry without error key", func(t *testing.T) {
		entry := &logrus.Entry{
			Level:   logrus.ErrorLevel,
			Message: "something went wrong",
			Data:    logrus.Fields{},
		}
		err := hook.Fire(entry)
		assert.NoError(t, err)
	})

	t.Run("fires on fatal entry", func(t *testing.T) {
		entry := &logrus.Entry{
			Level:   logrus.FatalLevel,
			Message: "fatal problem",
			Data: logrus.Fields{
				logrus.ErrorKey: fmt.Errorf("fatal error"),
			},
		}
		err := hook.Fire(entry)
		assert.NoError(t, err)
	})

	t.Run("includes metadata in extra", func(t *testing.T) {
		entry := &logrus.Entry{
			Level:   logrus.ErrorLevel,
			Message: "error with metadata",
			Data: logrus.Fields{
				logrus.ErrorKey: fmt.Errorf("test error"),
				"user_id":       123,
				"request_id":    "abc-123",
			},
		}
		err := hook.Fire(entry)
		assert.NoError(t, err)
	})
}

func TestSentryHookBuildEvent(t *testing.T) {
	hook := &sentryHook{}

	t.Run("plain error gets a fallback stacktrace", func(t *testing.T) {
		event := hook.buildEvent(&logrus.Entry{
			Level:   logrus.ErrorLevel,
			Message: "context",
			Data:    logrus.Fields{logrus.ErrorKey: fmt.Errorf("plain")},
		})
		assert.NotEmpty(t, event.Exception)
		outer := event.Exception[len(event.Exception)-1]
		assert.NotNil(t, outer.Stacktrace)
		assert.NotEmpty(t, outer.Stacktrace.Frames)
	})

	t.Run("entry without error key still gets a stacktrace", func(t *testing.T) {
		event := hook.buildEvent(&logrus.Entry{
			Level:   logrus.ErrorLevel,
			Message: "no error attached",
			Data:    logrus.Fields{},
		})
		assert.NotEmpty(t, event.Exception)
		outer := event.Exception[len(event.Exception)-1]
		assert.NotNil(t, outer.Stacktrace)
		assert.NotEmpty(t, outer.Stacktrace.Frames)
	})

	t.Run("ErrorWithStacktrace's captured frames win over fallback", func(t *testing.T) {
		wrapped := ErrorWithStacktrace(fmt.Errorf("inner"))
		var errSt *errorWithStacktrace
		assert.True(t, errors.As(wrapped, &errSt))

		event := hook.buildEvent(&logrus.Entry{
			Level:   logrus.ErrorLevel,
			Message: "ctx",
			Data:    logrus.Fields{logrus.ErrorKey: wrapped},
		})
		outer := event.Exception[len(event.Exception)-1]
		assert.Same(t, errSt.stacktrace, outer.Stacktrace)
	})

	t.Run("outermost exception type uses errorClass for grouping", func(t *testing.T) {
		base := &customError{msg: "boom"}
		event := hook.buildEvent(&logrus.Entry{
			Level:   logrus.ErrorLevel,
			Message: "ctx",
			Data:    logrus.Fields{logrus.ErrorKey: base},
		})
		outer := event.Exception[len(event.Exception)-1]
		assert.Equal(t, "*logging.customError", outer.Type)
	})

	t.Run("wrapped chain produces multiple exceptions", func(t *testing.T) {
		base := &customError{msg: "boom"}
		wrapped := fmt.Errorf("middle: %w", base)
		event := hook.buildEvent(&logrus.Entry{
			Level:   logrus.ErrorLevel,
			Message: "outer",
			Data:    logrus.Fields{logrus.ErrorKey: wrapped},
		})
		// hook's fmt.Errorf wrapping + middle wrap + base
		assert.Len(t, event.Exception, 3)
		assert.Equal(t, "*logging.customError", event.Exception[len(event.Exception)-1].Type)
	})

	t.Run("fatal level maps to sentry fatal", func(t *testing.T) {
		event := hook.buildEvent(&logrus.Entry{
			Level:   logrus.FatalLevel,
			Message: "dead",
			Data:    logrus.Fields{logrus.ErrorKey: fmt.Errorf("err")},
		})
		assert.Equal(t, sentry.LevelFatal, event.Level)
	})

	t.Run("non-error fields land in extra context", func(t *testing.T) {
		event := hook.buildEvent(&logrus.Entry{
			Level:   logrus.ErrorLevel,
			Message: "msg",
			Data: logrus.Fields{
				logrus.ErrorKey: fmt.Errorf("err"),
				"user_id":       42,
			},
		})
		extra, ok := event.Contexts["extra"]
		assert.True(t, ok)
		assert.Equal(t, 42, extra["user_id"])
		_, hasErr := extra[logrus.ErrorKey]
		assert.False(t, hasErr)
	})
}

func TestSentryHookLevels(t *testing.T) {
	hook := &sentryHook{}
	levels := hook.Levels()
	assert.Contains(t, levels, logrus.ErrorLevel)
	assert.Contains(t, levels, logrus.FatalLevel)
	assert.Contains(t, levels, logrus.PanicLevel)
	assert.NotContains(t, levels, logrus.WarnLevel)
	assert.NotContains(t, levels, logrus.InfoLevel)
	assert.NotContains(t, levels, logrus.DebugLevel)
}

func TestShouldNotify(t *testing.T) {
	t.Run("returns true when release stages is empty", func(t *testing.T) {
		assert.True(t, shouldNotify([]string{}, "production"))
	})

	t.Run("returns true when environment matches", func(t *testing.T) {
		assert.True(t, shouldNotify([]string{"production", "staging"}, "production"))
	})

	t.Run("returns false when environment does not match", func(t *testing.T) {
		assert.False(t, shouldNotify([]string{"production", "staging"}, "development"))
	})

	t.Run("returns true when nil release stages", func(t *testing.T) {
		assert.True(t, shouldNotify(nil, "production"))
	})
}

func TestNewWithSentry(t *testing.T) {
	logger := new(true, LoggingConfig{LogLevel: "INFO"})
	assert.NotNil(t, logger)

	hasSentryHook := false
	for _, hooks := range logger.Hooks {
		for _, hook := range hooks {
			if _, ok := hook.(*sentryHook); ok {
				hasSentryHook = true
			}
		}
	}
	assert.True(t, hasSentryHook, "logger should have sentry hook")
}

func TestNewWithoutSentry(t *testing.T) {
	logger := new(false, LoggingConfig{LogLevel: "INFO"})
	assert.NotNil(t, logger)

	hasSentryHook := false
	for _, hooks := range logger.Hooks {
		for _, hook := range hooks {
			if _, ok := hook.(*sentryHook); ok {
				hasSentryHook = true
			}
		}
	}
	assert.False(t, hasSentryHook, "logger should not have sentry hook")
}

func TestErrorWithStacktrace(t *testing.T) {
	t.Run("wraps error and preserves message", func(t *testing.T) {
		original := fmt.Errorf("something broke")
		wrapped := ErrorWithStacktrace(original)

		assert.Equal(t, "something broke", wrapped.Error())
		assert.True(t, errors.Is(wrapped, original))
	})

	t.Run("captures a sentry stacktrace", func(t *testing.T) {
		original := fmt.Errorf("boom")
		wrapped := ErrorWithStacktrace(original)

		var errSt *errorWithStacktrace
		assert.True(t, errors.As(wrapped, &errSt))
		assert.NotNil(t, errSt.stacktrace)
		assert.NotEmpty(t, errSt.stacktrace.Frames)
	})

	t.Run("sentry hook attaches stacktrace to event", func(t *testing.T) {
		original := fmt.Errorf("traced error")
		wrapped := ErrorWithStacktrace(original)

		hook := &sentryHook{}
		entry := &logrus.Entry{
			Level:   logrus.ErrorLevel,
			Message: "test",
			Data: logrus.Fields{
				logrus.ErrorKey: wrapped,
			},
		}
		err := hook.Fire(entry)
		assert.NoError(t, err)
	})

	t.Run("sentry hook works without stacktrace", func(t *testing.T) {
		hook := &sentryHook{}
		entry := &logrus.Entry{
			Level:   logrus.ErrorLevel,
			Message: "plain error",
			Data: logrus.Fields{
				logrus.ErrorKey: fmt.Errorf("no stack"),
			},
		}
		err := hook.Fire(entry)
		assert.NoError(t, err)
	})
}

func TestErrorf(t *testing.T) {
	t.Run("formats and wraps error", func(t *testing.T) {
		inner := fmt.Errorf("db miss")
		err := Errorf("user %d not found: %w", 42, inner)
		assert.Equal(t, "user 42 not found: db miss", err.Error())
		assert.True(t, errors.Is(err, inner))
	})

	t.Run("captures a sentry stacktrace", func(t *testing.T) {
		err := Errorf("boom")
		var errSt *errorWithStacktrace
		assert.True(t, errors.As(err, &errSt))
		assert.NotNil(t, errSt.stacktrace)
		assert.NotEmpty(t, errSt.stacktrace.Frames)
	})

	t.Run("top stacktrace frame is the caller, not Errorf", func(t *testing.T) {
		err := Errorf("boom")
		var errSt *errorWithStacktrace
		assert.True(t, errors.As(err, &errSt))
		top := errSt.stacktrace.Frames[len(errSt.stacktrace.Frames)-1]
		assert.NotEqual(t, "Errorf", top.Function)
	})
}

func TestErrorWithStacktracePreservesUnwrap(t *testing.T) {
	inner := fmt.Errorf("inner")
	outer := fmt.Errorf("outer: %w", inner)
	wrapped := ErrorWithStacktrace(outer)

	assert.True(t, errors.Is(wrapped, inner))
}

func TestSentryHookExtractsStacktraceFromWrappedMessage(t *testing.T) {
	original := fmt.Errorf("deep error")
	wrapped := ErrorWithStacktrace(original)

	hook := &sentryHook{}
	entry := &logrus.Entry{
		Level:   logrus.ErrorLevel,
		Message: "context message",
		Data: logrus.Fields{
			logrus.ErrorKey: wrapped,
		},
	}

	err := hook.Fire(entry)
	assert.NoError(t, err)

	var errSt *errorWithStacktrace
	wrappedEntry := fmt.Errorf("%s: %w", entry.Message, wrapped)
	assert.True(t, errors.As(wrappedEntry, &errSt))
	assert.NotNil(t, errSt.stacktrace)
	assert.NotEmpty(t, errSt.stacktrace.Frames)
}

type customError struct{ msg string }

func (e *customError) Error() string { return e.msg }

func TestErrorClassUnwrapsWrappers(t *testing.T) {
	t.Run("returns underlying type for fmt-wrapped error", func(t *testing.T) {
		base := &customError{msg: "boom"}
		wrapped := fmt.Errorf("context: %w", base)
		assert.Equal(t, "*logging.customError", errorClass(wrapped))
	})

	t.Run("returns underlying type for errorWithStacktrace", func(t *testing.T) {
		base := &customError{msg: "boom"}
		wrapped := ErrorWithStacktrace(base)
		assert.Equal(t, "*logging.customError", errorClass(wrapped))
	})

	t.Run("returns underlying type for nested wrappers", func(t *testing.T) {
		base := &customError{msg: "boom"}
		wrapped := fmt.Errorf("outer: %w", ErrorWithStacktrace(fmt.Errorf("inner: %w", base)))
		assert.Equal(t, "*logging.customError", errorClass(wrapped))
	})

	t.Run("returns wrapper type when chain terminates without unwrapping", func(t *testing.T) {
		bare := fmt.Errorf("just a string")
		assert.Equal(t, "*errors.errorString", errorClass(bare))
	})
}

func TestNSQLogger(t *testing.T) {
	Log.Level = logrus.DebugLevel
	_, level := Log.NSQLogger()
	assert.EqualValues(t, level, nsq.LogLevelDebug)

	Log.Level = logrus.InfoLevel
	_, level = Log.NSQLogger()
	assert.EqualValues(t, level, nsq.LogLevelInfo)

	Log.Level = logrus.WarnLevel
	_, level = Log.NSQLogger()
	assert.EqualValues(t, level, nsq.LogLevelWarning)

	Log.Level = logrus.ErrorLevel
	_, level = Log.NSQLogger()
	assert.EqualValues(t, level, nsq.LogLevelError)

	Log.Level = logrus.FatalLevel
	_, level = Log.NSQLogger()
	assert.EqualValues(t, level, nsq.LogLevelError)

	Log.Level = logrus.PanicLevel
	_, level = Log.NSQLogger()
	assert.EqualValues(t, level, nsq.LogLevelError)
}
