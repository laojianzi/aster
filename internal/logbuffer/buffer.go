package logbuffer

import (
	"strings"
	"sync"
	"unicode/utf8"
)

// Buffer bounds both retained lines and encoded bytes. Truncation is explicit.
type Buffer struct { mu sync.Mutex; lines []string; bytes,maxBytes,maxLines int; dropped uint64; revision uint64 }
func New(maxLines,maxBytes int)*Buffer { if maxLines<=0 || maxBytes<64 { panic("invalid log budget") };return &Buffer{maxLines:maxLines,maxBytes:maxBytes} }
func (b *Buffer) Append(line string) {
	b.mu.Lock(); defer b.mu.Unlock()
	line=strings.ToValidUTF8(line,"�")
	if len(line)>b.maxBytes { n:=b.maxBytes-20; for n>0 && !utf8.RuneStart(line[n]) { n-- };line=line[:n]+" [line truncated]";b.dropped++ }
	for len(b.lines)>0 && (len(b.lines)>=b.maxLines || b.bytes+len(line)>b.maxBytes) { b.bytes-=len(b.lines[0]);b.lines[0]="";b.lines=b.lines[1:];b.dropped++ }
	b.lines=append(b.lines,line);b.bytes+=len(line);b.revision++
}
func (b *Buffer) Snapshot()([]string,uint64,uint64) { b.mu.Lock();defer b.mu.Unlock();return append([]string(nil),b.lines...),b.dropped,b.revision }
func (b *Buffer) Clear(){ b.mu.Lock();defer b.mu.Unlock();b.lines=nil;b.bytes=0;b.dropped=0;b.revision++ }
