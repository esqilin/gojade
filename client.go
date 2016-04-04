// IDEA: subtype of sound FilteredSound accepts filter
// FilterChain is a subtype of Filter
// Filter implements interface Sound which has method Sample which returns float
// "done" channel
package jade

// TODO: go through all classes and check if we always need methods on pointers
// because direct values are cheaper
// TODO: interrupt attack / decay, when envelope is released before sustain
// TODO: replace ALL maps by slices (performance! maps suck)
// TODO: improve xrun handling: LPF; increase latency & use point average
// TODO: check for NULL return values in JACK functions

import (
	"github.com/esqilin/godsp"
	"github.com/esqilin/godsp/bus"
	"github.com/xthexder/go-jack"

	"fmt"
	"os"
)

type Client struct {
	Jack   *jack.Client
	master *bus.ConcurrentBus
	portsIn   []audioInPort
	portsOut  []audioOutPort
	midisIn   []midiInPort
}

// New returns a client with JACK client name (if unassigned)
//
// if startJack is true and JACK is not running, the server will be started
func New(name string, startJack bool) (c *Client, err error) {
    jackOptions := jack.NoStartServer
    if startJack {
        jackOptions = jack.NullOption
    }

	jc, status := jack.ClientOpen(name, jackOptions)
	defer func() {
		if nil != jc && nil != err {
			jc.Close()
		}
	}()
    if nil == jc {
        if 0 != status {
            err = jack.Strerror(status)
        } else {
            err = fmt.Errorf("unknown error opening JACK client")
        }
        return nil, err
    }

	dsp.Init(jc.GetSampleRate())

	master := bus.NewConcurrent()
	c = &Client{
		Jack:   jc,
		master: master,
	}

    status = jc.SetProcessCallback(c.process)
    if 0 != status {
        err = jack.Strerror(status)
        return c, fmt.Errorf("error setting JACK process callback: %s", err)
    }

	status = c.Jack.Activate()
    if 0 != status {
        err = jack.Strerror(status)
        err = fmt.Errorf("error activating JACK client: %s", err)
    }

	return c, err
}

func (c Client) Close() {
	c.Jack.Close()
}

// PlaySound important: sounds must not use any common Sampleable objects
// because they are sampled concurrently
func (c Client) PlaySound(s dsp.Sound) error {
	c.master.PlaySound(s)
	return nil
}

func (c Client) Play(ch chan<- float64) {
    go func (s dsp.Sound, ch chan<- float64) {
        for { // TODO: add "done" channel
            ch <- s.Sample()
        }
    }(c.master, ch)
}


func (c *Client) AddAudioOut(name string, bufferRatio float32) chan<- float64 {
    p := c.Jack.PortRegister(name, jack.DEFAULT_AUDIO_TYPE, jack.PortIsOutput, 0)
    n := int(float32(c.Jack.GetBufferSize()) * (1.0 + bufferRatio))
    ch := make(chan float64, n)
    c.portsOut = append(c.portsOut, audioOutPort{ p, ch })
    return ch
}

func (c *Client) AddAudioIn(name string) <-chan float64  {
	p := c.Jack.PortRegister(name, jack.DEFAULT_AUDIO_TYPE, jack.PortIsInput, 0)
    ch := make(chan float64, c.Jack.GetBufferSize())
    c.portsIn = append(c.portsIn, audioInPort{ p, ch })
    return ch
}

func (c *Client) AddMidiIn(name string) chan jack.MidiData {
    p := c.Jack.PortRegister(name, jack.DEFAULT_MIDI_TYPE, jack.PortIsInput, 0)
    ch := make(chan jack.MidiData, c.Jack.GetBufferSize())
    c.midisIn = append(c.midisIn, midiInPort{ p, ch })
    return ch
}

func (c Client) Connect(src, dst string) error {
    status := c.Jack.Connect(src, dst)
    if 0 != status {
        return fmt.Errorf("JACK: %s", jack.Strerror(status))
    }
    return nil
}

func logError(msg string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, msg+"\n", a...)
}

func (c *Client) process(nFrames uint32) int {
    hasXrun := false

    // write inputs to channels
    for _, p := range c.portsIn {
        ss := p.jackPort.GetBuffer(nFrames)
        for _, s := range ss {
            select {
            case p.ch <- float64(s):
            default:
                hasXrun = true
            }
        }
    }
    if hasXrun {
        logError("XRUN: input channel full")
    }

    hasXrun = false
    // write MIDI events to channel
    for _, p := range c.midisIn {
        es := p.jackPort.GetMidiEvents(nFrames)
        for _, e := range es {
            select {
            case p.ch <- *e:
            default:
                hasXrun = true
            }
        }
    }
    if hasXrun {
        logError("XRUN: MIDI channel full")
    }

    hasXrun = false
    var s0 jack.AudioSample // for sample and hold (make clipping lower)
    // read channel data into output port
    for _, p := range c.portsOut {
        ss := p.jackPort.GetBuffer(nFrames)
        for i, _ := range ss {
            select {
            case s := <-p.ch:
                s0 = jack.AudioSample(s)
            default:
                hasXrun = true
            }
            ss[i] = s0
        }
    }
    if hasXrun {
        logError("XRUN: output channel empty")
    }

    return 0
}
