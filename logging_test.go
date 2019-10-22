package logging

import (
	"io/ioutil"
	"os"
	"strings"
	"sync"
	"testing"

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
