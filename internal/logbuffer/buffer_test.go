package logbuffer

import (
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)
func TestBudgetsAndOwnership(t *testing.T) {
	b:=New(3,64)
	for i:=0;i<20;i++{b.Append("line")}
	lines,dropped,_:=b.Snapshot();if len(lines)!=3 || dropped!=17{t.Fatalf("%v %d",lines,dropped)}
	lines[0]="mutated";again,_,_:=b.Snapshot();if again[0]=="mutated"{t.Fatal("snapshot aliases buffer")}
	b.Append(strings.Repeat("原生",100));lines,dropped,_=b.Snapshot()
	for _,line:=range lines{if !utf8.ValidString(line)||len(line)>64{t.Fatal("invalid truncation")}}
	if dropped==0{t.Fatal("no truncation marker")}
}
func TestConcurrentReadersAndWriter(t *testing.T){b:=New(10,1024);var wg sync.WaitGroup;for i:=0;i<4;i++{wg.Add(1);go func(){defer wg.Done();for n:=0;n<1000;n++{b.Append("event");b.Snapshot()}}()};wg.Wait()}
