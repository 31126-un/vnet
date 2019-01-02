package log

import (
	"github.com/rc452860/vnet/utils"
)

type Logging struct {
	Name                   string
	Level                  string
	LogFormatterWritePairs []LogFormatterWritePair
}

type LogFormatterWritePair struct {
	Formatter LogFormatter
	Writer    LogWriter
}
type LogFormatter interface {
	Format(message string, level string, params ...interface{}) string
}

type LogWriter interface {
	Write(message string)
}

const (
	DEBUG = "DEBUG"
	INFO  = "INFO"
	WARN  = "WARN"
	ERROR = "ERROR"
)

var LevelMap = map[string]int{
	DEBUG: 1,
	INFO:  2,
	WARN:  3,
	ERROR: 4,
}

var Level []string = []string{
	DEBUG,
	INFO,
	WARN,
	ERROR,
}

const (
	LEVEL   = "%{level}"
	TIME    = "%{time}"
	FILE    = "%{file}"
	FUNC    = "%{func}"
	LINENO  = "%{line}"
	MESSAGE = "%{message}"
)

type ILog interface {
	Debug(message string)
	Info(message string)
	Warn(message string)
	Error(message string)
	Err(err error)
}

var Loggers map[string]*Logging

func init() {
	Loggers = make(map[string]*Logging)
}

func GetLogger(name string, level ...string) *Logging {
	if Loggers[name] != nil {
		return Loggers[name]
	}
	var levelSwap string
	if len(level) != 0 {
		levelSwap = level[0]
	}
	if utils.StringArrayContain(Level, levelSwap) {
		// do nothing
	} else {
		levelSwap = DEBUG
	}

	log := &Logging{
		Name:  name,
		Level: levelSwap,
		LogFormatterWritePairs: []LogFormatterWritePair{

			LogFormatterWritePair{
				Writer:    LogTerminalWriterFactory(),
				Formatter: PatternLogFormatterFactory(),
			},
		},
	}
	Loggers[name] = log
	return log
}

func (this *Logging) Debug(message string, params ...interface{}) {
	this.write(DEBUG, message, params...)
}

func (this *Logging) Info(message string, params ...interface{}) {
	this.write(INFO, message, params...)
}

func (this *Logging) Warn(message string, params ...interface{}) {
	this.write(WARN, message, params...)
}

func (this *Logging) Error(message string, params ...interface{}) {
	this.write(ERROR, message, params...)
}
func (this *Logging) Err(err error) {
	this.write(ERROR, err.Error())
}
func (this *Logging) write(level string, message string, params ...interface{}) {
	if LevelMap[level] < LevelMap[this.Level] {
		return
	}
	for _, item := range this.LogFormatterWritePairs {
		formatString := item.Formatter.Format(message, level, params...)
		item.Writer.Write(formatString)
	}
}
