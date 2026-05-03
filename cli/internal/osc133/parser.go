// Package osc133 是 OSC 133 prompt-boundary 暗号的流式解析器。
//
// 输入:PTY 输出的原始字节流(可能含终端控制序列)
// 输出:
//  1. 清理后的字节(剥离 OSC 133 序列后)写到 downstream(供用户屏幕渲染)
//  2. 命令事件(A/B/C/D)通过 OnEvent 回调通知调用方
//
// 识别的序列形态:
//
//	ESC ] 133 ; A BEL                prompt 开始
//	ESC ] 133 ; B BEL                prompt 结束 / 用户输入开始(M5 才用)
//	ESC ] 133 ; C BEL                命令开始执行
//	ESC ] 133 ; D ; <exit> BEL       命令结束(带退出码)
//
// 其他 OSC 序列(例如 `ESC ] 0 ; <title> BEL` 设置终端标题)会原样
// 透传给 downstream,保证终端行为不被破坏。ANSI CSI(ESC [ ...)也不受影响。
//
// 解析器是纯 stream-based,状态跨多次 Write 保持 —— OSC 序列被切在
// 任意字节边界都能正确识别。
package osc133

import (
	"io"
	"strconv"
	"strings"
)

const (
	esc = 0x1B // ESC
	bel = 0x07 // BEL

	// maxOscPayload 是 OSC 参数的最大字节数。超过就当奇怪输入 fallback
	// 到普通字节流,避免攻击/bug 无限增长内存。
	maxOscPayload = 2 * 1024
)

// EventKind 是事件类型。
type EventKind int

const (
	EventPromptStart  EventKind = iota + 1 // A
	EventPromptEnd                         // B
	EventCommandStart                      // C
	EventCommandEnd                        // D
)

func (k EventKind) String() string {
	switch k {
	case EventPromptStart:
		return "PromptStart"
	case EventPromptEnd:
		return "PromptEnd"
	case EventCommandStart:
		return "CommandStart"
	case EventCommandEnd:
		return "CommandEnd"
	}
	return "Unknown"
}

// Event 是 parser 识别到的一次 OSC 133 暗号。
type Event struct {
	Kind EventKind
	// Command 仅在 Kind == EventCommandStart 时可能存在。
	Command string
	// Exit 仅在 Kind == EventCommandEnd 时有效
	Exit int
}

// OnEvent 是事件回调。Parser 内部同步调用 handler,handler 应当尽快返回。
type OnEvent func(Event)

// Parser 是一个 io.Writer:写入 PTY 原始字节,清理后的字节流到 downstream,
// OSC 133 暗号转成 Event 回调。
type Parser struct {
	downstream io.Writer
	onEvent    OnEvent

	state parseState

	// normalBuf 批量缓存 Normal 状态的字节,在遇到 ESC 或 Write 调用结束时
	// 一次性 flush 到 downstream,避免每字节一次 syscall。
	normalBuf []byte

	// oscBuf 累积 OSC 参数(不含 `ESC ]` 前缀和 BEL/ST 终止符)。
	oscBuf []byte
}

type parseState int

const (
	stateNormal parseState = iota
	stateEsc               // 看到 ESC,等下一字节
	stateOsc               // 在 OSC 序列里(ESC ] 后)
	stateOscEsc            // OSC 里看到 ESC(可能是 ESC\ 终止)
)

// NewParser 构造一个 Parser。
// downstream 接收清理后的字节;onEvent 接收 OSC 133 事件,允许为 nil。
func NewParser(downstream io.Writer, onEvent OnEvent) *Parser {
	return &Parser{
		downstream: downstream,
		onEvent:    onEvent,
	}
}

// Write 实现 io.Writer。
//
// 返回值遵循 io.Writer 契约:n == len(p),除非 downstream 写入失败才返 n<len。
// Parser "接受"所有输入字节 —— 有些写到 downstream,有些被 OSC 吞掉。
func (p *Parser) Write(in []byte) (int, error) {
	for _, b := range in {
		if p.state == stateNormal && b != esc {
			p.normalBuf = append(p.normalBuf, b)
			continue
		}
		// 进入状态机之前先 flush 正常字节,保证 downstream 顺序正确
		if err := p.flushNormal(); err != nil {
			return 0, err
		}
		if err := p.step(b); err != nil {
			return 0, err
		}
	}
	// Write 结束时如果还在 Normal state,就把 buffer 刷光;
	// 否则留着等下一次 Write 或 OSC 收尾。
	if p.state == stateNormal {
		if err := p.flushNormal(); err != nil {
			return 0, err
		}
	}
	return len(in), nil
}

func (p *Parser) flushNormal() error {
	if len(p.normalBuf) == 0 {
		return nil
	}
	_, err := p.downstream.Write(p.normalBuf)
	p.normalBuf = p.normalBuf[:0]
	return err
}

// step 处理 non-Normal 状态 或 Normal 状态下的 ESC 字节。
func (p *Parser) step(b byte) error {
	switch p.state {
	case stateNormal:
		// 只可能是 ESC(Normal 下的非 ESC 已被 Write 批量累积,不会走到 step)
		p.state = stateEsc
		return nil

	case stateEsc:
		switch b {
		case ']':
			p.state = stateOsc
			p.oscBuf = p.oscBuf[:0]
		case esc:
			// 前一个 ESC 是孤立的,先 flush 它,新 ESC 保持 pending
			if err := p.writeByte(esc); err != nil {
				return err
			}
			// state 保持 stateEsc
		default:
			// 其他 ESC+X(比如 CSI 的 ESC [ ...)原样 passthrough
			if err := p.writeByte(esc); err != nil {
				return err
			}
			if err := p.writeByte(b); err != nil {
				return err
			}
			p.state = stateNormal
		}
		return nil

	case stateOsc:
		switch b {
		case bel:
			return p.finishOsc()
		case esc:
			p.state = stateOscEsc
			return nil
		default:
			if len(p.oscBuf) >= maxOscPayload {
				// OSC 太长,放弃解析,把 ESC ] + oscBuf + 当前字节 当普通字节 flush
				if err := p.writeByte(esc); err != nil {
					return err
				}
				if err := p.writeByte(']'); err != nil {
					return err
				}
				if _, err := p.downstream.Write(p.oscBuf); err != nil {
					return err
				}
				p.oscBuf = p.oscBuf[:0]
				p.state = stateNormal
				// 当前字节继续当普通字节处理
				return p.writeByte(b)
			}
			p.oscBuf = append(p.oscBuf, b)
			return nil
		}

	case stateOscEsc:
		if b == '\\' {
			// ESC\ 终止 OSC
			return p.finishOsc()
		}
		// 不是 ST,把那个 ESC + 当前字节当作 OSC payload 的一部分,回到 OSC 状态
		p.oscBuf = append(p.oscBuf, esc, b)
		p.state = stateOsc
		return nil
	}
	return nil
}

// finishOsc 在 OSC 序列被终止(BEL 或 ESC\)时调用。
// 如果 oscBuf 是我们认识的 133;X[;arg],发事件;否则原样 passthrough。
func (p *Parser) finishOsc() error {
	defer func() {
		p.oscBuf = p.oscBuf[:0]
		p.state = stateNormal
	}()

	if ev, ok := parse133(p.oscBuf); ok {
		if p.onEvent != nil {
			p.onEvent(ev)
		}
		return nil
	}
	// 不是 133,原样透传 ESC ] <payload> BEL
	if err := p.writeByte(esc); err != nil {
		return err
	}
	if err := p.writeByte(']'); err != nil {
		return err
	}
	if _, err := p.downstream.Write(p.oscBuf); err != nil {
		return err
	}
	return p.writeByte(bel)
}

func (p *Parser) writeByte(b byte) error {
	_, err := p.downstream.Write([]byte{b})
	return err
}

// parse133 尝试把 OSC payload 识别为 `133;<letter>[;<arg>]`。
// 成功返回 (ev, true),否则 (_, false)。
func parse133(payload []byte) (Event, bool) {
	const prefix = "133;"
	s := string(payload)
	if !strings.HasPrefix(s, prefix) {
		return Event{}, false
	}
	rest := s[len(prefix):]

	var letter, arg string
	if i := strings.IndexByte(rest, ';'); i >= 0 {
		letter = rest[:i]
		arg = rest[i+1:]
	} else {
		letter = rest
	}

	switch letter {
	case "A":
		return Event{Kind: EventPromptStart}, true
	case "B":
		return Event{Kind: EventPromptEnd}, true
	case "C":
		return Event{Kind: EventCommandStart, Command: arg}, true
	case "D":
		ev := Event{Kind: EventCommandEnd}
		if arg != "" {
			// arg 里可能还有更多 ; 分隔的字段,只取第一个当 exit
			if j := strings.IndexByte(arg, ';'); j >= 0 {
				arg = arg[:j]
			}
			if n, err := strconv.Atoi(arg); err == nil {
				ev.Exit = n
			}
		}
		return ev, true
	}
	return Event{}, false
}
