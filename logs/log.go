package logs

import (
	"bytes"
	"dex/pb"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

                                                                                    
                                                                              
var (
	outSink = &writerSink{w: os.Stdout}
	errSink = &writerSink{w: os.Stderr}
)

type writerSink struct {
	mu sync.Mutex
	w  io.Writer
}

const DefaultLogFileMaxBytes int64 = 2 << 30

type rollingFile struct {
	mu           sync.Mutex
	path         string
	file         *os.File
	size         int64
	segmentBytes int64
}

func (s *writerSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

func (s *writerSink) attach(extra io.Writer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.w = io.MultiWriter(s.w, extra)
}

                                                                                          
func EnableFileOutput(path string) error {
	f, err := newRollingFile(path, DefaultLogFileMaxBytes)
	if err != nil {
		return err
	}
	outSink.attach(f)
	errSink.attach(f)
	return nil
}

func newRollingFile(path string, maxBytes int64) (*rollingFile, error) {
	if maxBytes < 2 {
		return nil, fmt.Errorf("log max bytes must be at least 2")
	}
	w := &rollingFile{path: path, segmentBytes: maxBytes / 2}
	if err := os.Remove(path + ".1"); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err := w.openTruncate(); err != nil {
		return nil, err
	}
	return w, nil
}

                                                                           
                                                                           
                                                         
func NewRollingWriter(path string, maxBytes int64) (io.WriteCloser, error) {
	return newRollingFile(path, maxBytes)
}

func (w *rollingFile) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	originalLen := len(p)
	if int64(len(p)) > w.segmentBytes {
		p = p[len(p)-int(w.segmentBytes):]
	}
	if w.size > 0 && w.size+int64(len(p)) > w.segmentBytes {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	if err != nil {
		return n, err
	}
	if n != len(p) {
		return n, io.ErrShortWrite
	}
	return originalLen, nil
}

func (w *rollingFile) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *rollingFile) openTruncate() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	w.file = f
	w.size = 0
	return nil
}

func (w *rollingFile) rotate() error {
	if err := w.file.Close(); err != nil {
		return err
	}
	w.file = nil
	if err := os.Remove(w.path + ".1"); err != nil && !os.IsNotExist(err) {
		_ = w.openTruncate()
		return err
	}
	if err := os.Rename(w.path, w.path+".1"); err != nil {
		_ = w.openTruncate()
		return err
	}
	return w.openTruncate()
}

                                                            
const (
	LevelTrace   = iota                             
	LevelDebug              
	LevelVerbose                              
	LevelInfo               
	LevelWarning            
	LevelError                                      
)

var logLevel = LevelInfo                      

                                                                            
type Logger interface {
	Trace(format string, v ...interface{})
	Debug(format string, v ...interface{})
	Verbose(format string, v ...interface{})
	Info(format string, v ...interface{})
	Warn(format string, v ...interface{})
	Error(format string, v ...interface{})
	GetBuffer() []*pb.LogLine
}

                                                                          
var Registry = struct {
	mu      sync.RWMutex
	loggers map[string]Logger
}{
	loggers: make(map[string]Logger),
}

                                                                      
type NodeLogger struct {
	addr          string
	traceLogger   *log.Logger
	debugLogger   *log.Logger
	verboseLogger *log.Logger
	infoLogger    *log.Logger
	warnLogger    *log.Logger
	errorLogger   *log.Logger

	mu       sync.RWMutex
	buffer   []*pb.LogLine
	maxLines int
}

                                                            
func NewNodeLogger(address string, maxLines int) *NodeLogger {
	prefix := address
	if len(address) > 7 {
		prefix = address[:7]
	}
	l := &NodeLogger{
		addr:          address,
		traceLogger:   log.New(outSink, fmt.Sprintf("[TRACE]   [%s] ", prefix), log.Ldate|log.Ltime|log.Lmicroseconds),
		debugLogger:   log.New(outSink, fmt.Sprintf("[DEBUG]   [%s] ", prefix), log.Ldate|log.Ltime|log.Lmicroseconds),
		verboseLogger: log.New(outSink, fmt.Sprintf("[VERBOSE] [%s] ", prefix), log.Ldate|log.Ltime|log.Lmicroseconds),
		infoLogger:    log.New(outSink, fmt.Sprintf("[INFO]    [%s] ", prefix), log.Ldate|log.Ltime|log.Lmicroseconds),
		warnLogger:    log.New(outSink, fmt.Sprintf("[WARN]    [%s] ", prefix), log.Ldate|log.Ltime|log.Lmicroseconds),
		errorLogger:   log.New(errSink, fmt.Sprintf("[ERROR]   [%s] ", prefix), log.Ldate|log.Ltime|log.Lmicroseconds),
		maxLines:      maxLines,
		buffer:        make([]*pb.LogLine, 0, maxLines),
	}

	Registry.mu.Lock()
	Registry.loggers[address] = l
	Registry.mu.Unlock()

	return l
}

func (l *NodeLogger) writeToBuffer(level, message string) {
	if l.maxLines <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	                                                            
	                                                            
	validMessage := strings.ToValidUTF8(message, "")

	line := &pb.LogLine{
		Timestamp: time.Now().Format("15:04:05.000"),
		Level:     level,
		Message:   validMessage,
	}

	if len(l.buffer) >= l.maxLines {
		                                                                                                  
		copy(l.buffer, l.buffer[1:])
		l.buffer[len(l.buffer)-1] = line
	} else {
		l.buffer = append(l.buffer, line)
	}
}

func (l *NodeLogger) GetBuffer() []*pb.LogLine {
	l.mu.RLock()
	defer l.mu.RUnlock()
	res := make([]*pb.LogLine, len(l.buffer))
	copy(res, l.buffer)
	return res
}

func (l *NodeLogger) Trace(format string, v ...interface{}) {
	if logLevel <= LevelTrace {
		msg := fmt.Sprintf(format, v...)
		l.traceLogger.Print(msg)
		l.writeToBuffer("TRACE", msg)
	}
}

func (l *NodeLogger) Debug(format string, v ...interface{}) {
	if logLevel <= LevelDebug {
		msg := fmt.Sprintf(format, v...)
		l.debugLogger.Print(msg)
		l.writeToBuffer("DEBUG", msg)
	}
}

func (l *NodeLogger) Verbose(format string, v ...interface{}) {
	if logLevel <= LevelVerbose {
		msg := fmt.Sprintf(format, v...)
		l.verboseLogger.Print(msg)
		l.writeToBuffer("VERBOSE", msg)
	}
}

func (l *NodeLogger) Info(format string, v ...interface{}) {
	if logLevel <= LevelInfo {
		msg := fmt.Sprintf(format, v...)
		l.infoLogger.Print(msg)
		l.writeToBuffer("INFO", msg)
	}
}

func (l *NodeLogger) Warn(format string, v ...interface{}) {
	if logLevel <= LevelWarning {
		msg := fmt.Sprintf(format, v...)
		l.warnLogger.Print(msg)
		l.writeToBuffer("WARN", msg)
	}
}

func (l *NodeLogger) Error(format string, v ...interface{}) {
	if logLevel <= LevelError {
		msg := fmt.Sprintf(format, v...)
		l.errorLogger.Print(msg)
		l.writeToBuffer("ERROR", msg)
	}
}

                                                                                             
var defaultLogger = NewNodeLogger("system", 2000)

                                                                             
var GIDToLoggerMapping sync.Map

                                                                             
                                                                              
                                                                      
                                                              
func SetThreadLogger(l Logger) func() {
	gid := getGID()
	previous, hadPrevious := GIDToLoggerMapping.Load(gid)
	GIDToLoggerMapping.Store(gid, l)

	var once sync.Once
	return func() {
		once.Do(func() {
			if hadPrevious {
				GIDToLoggerMapping.Store(gid, previous)
				return
			}
			GIDToLoggerMapping.Delete(gid)
		})
	}
}

func getActiveLogger() Logger {
	if l, ok := GIDToLoggerMapping.Load(getGID()); ok {
		return l.(Logger)
	}
	return defaultLogger
}

                                                                        
                                                                                                       
func SetThreadNodeContext(nodeIDOrAddr string) func() {
	                                         
	Registry.mu.RLock()
	l, ok := Registry.loggers[normalizeNodeIdent(nodeIDOrAddr)]
	Registry.mu.RUnlock()
	if !ok {
		return func() {}
	}
	return SetThreadLogger(l)
}

                                                                     
func RegisterNodeMapping(id string, address string) {
	                              
	idToAddrStatic.Store(id, address)
	                                                                                                   
	idToAddrStatic.Store(address, address)
}

var idToAddrStatic sync.Map

func normalizeNodeIdent(ident string) string {
	if addr, ok := idToAddrStatic.Load(ident); ok {
		return addr.(string)
	}
	if idx := strings.LastIndex(ident, ":"); idx != -1 {
		port := ident[idx+1:]
		if addr, ok := idToAddrStatic.Load(port); ok {
			return addr.(string)
		}
	}
	return ident
}

                                                                                                         
var gidBufPool = sync.Pool{New: func() any { b := make([]byte, 64); return &b }}

func getGID() uint64 {
	bp := gidBufPool.Get().(*[]byte)
	b := (*bp)[:cap(*bp)]
	b = b[:runtime.Stack(b, false)]
	b = bytes.TrimPrefix(b, []byte("goroutine "))
	b = b[:bytes.IndexByte(b, ' ')]
	n, _ := strconv.ParseUint(string(b), 10, 64)
	gidBufPool.Put(bp)
	return n
}

                                                                   
func GetLogsForNode(nodeIdent string) []*pb.LogLine {
	addr := normalizeNodeIdent(nodeIdent)
	Registry.mu.RLock()
	defer Registry.mu.RUnlock()
	if l, ok := Registry.loggers[addr]; ok {
		return l.GetBuffer()
	}
	return []*pb.LogLine{}
}

func GetAllLoggedNodes() []string {
	Registry.mu.RLock()
	defer Registry.mu.RUnlock()
	nodes := make([]string, 0, len(Registry.loggers))
	for k := range Registry.loggers {
		nodes = append(nodes, k)
	}
	return nodes
}

func GetLogs() []*pb.LogLine {
	return defaultLogger.GetBuffer()
}

                                                       
                                                                                                           
func Trace(format string, v ...interface{}) {
	if logLevel <= LevelTrace {
		getActiveLogger().Trace(format, v...)
	}
}
func Debug(format string, v ...interface{}) {
	if logLevel <= LevelDebug {
		getActiveLogger().Debug(format, v...)
	}
}
func Verbose(format string, v ...interface{}) {
	if logLevel <= LevelVerbose {
		getActiveLogger().Verbose(format, v...)
	}
}
func Info(format string, v ...interface{}) {
	if logLevel <= LevelInfo {
		getActiveLogger().Info(format, v...)
	}
}
func Warn(format string, v ...interface{}) {
	if logLevel <= LevelWarning {
		getActiveLogger().Warn(format, v...)
	}
}
func Error(format string, v ...interface{}) {
	if logLevel <= LevelError {
		getActiveLogger().Error(format, v...)
	}
}

                        
func (l *NodeLogger) writeToBufferOld(level, message string) {}
