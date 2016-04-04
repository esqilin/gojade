// IDEA: ConcurrentBus.AddSound gives dsp.Sound a channel it can write to
// once the sound has terminated it sends EOF to channel and Bus discards channel
// subtype of sound FilteredSound accepts filter
// FilterChain is a subtype of Filter
// Filter implements interface Sound which has method Sample which returns float
// boolean for "eof/done"-flag
// a ConcurrentBusSampler created in ConcurrentBus.AddSound could then call
// Sample() on its Sound object until the channel is full
// Filters always have a reference to their input Sound object, call their
// Sample() functions and potentially buffer samples
package gojade

// TODO: go through all classes and check if we always need methods on pointers
// because direct values are cheaper
// TODO: ensure jack returns nil if we got a nil client (i.e. server not running)

import (
	"github.com/esqilin/godsp"
	"github.com/esqilin/godsp/bus"
	"github.com/esqilin/godsp/sound"
	"github.com/esqilin/gojack"

	"fmt"
	"os"
)

type Client struct {
	jack   *gojack.Client
	master *bus.ConcurrentBus
	buf    *sound.BufferedSound
	aIns   map[string]*gojack.AudioInPort
	aOuts  map[string]*gojack.AudioOutPort
	mIns   map[string]*gojack.MidiInPort
}

//~ type MidiCallback func(byte, byte) // export it
type MidiEvent gojack.MidiEvent

func (e MidiEvent) IsOn() bool {
	return gojack.MIDI_NOTE_ON == e.Status
}

func (e MidiEvent) IsOff() bool {
	return gojack.MIDI_NOTE_OFF == e.Status
}

// New returns a client with JACK client name (if unassigned)
//
// if startJack is true and JACK is not running, the server will be started
func New(name string, startJack bool) (c *Client, err error) {
	var jc *gojack.Client
	jc, err = gojack.NewClient(name)
	defer func() {
		if nil != err && nil != jc {
			jc.Close()
		}
	}()

	if nil != err {
		return nil, err
	}

	if !startJack {
		jc.SetOptionNoStartServer()
	}

	if nil != jc.Open() {
		return nil, err
	}

	if jc.IsNameNotUnique() {
		logError(
			"jack client name not unique; using `%s'",
			jc.Name(),
		)
	}

	dsp.Init(jc.SampleRate())

	master := bus.NewConcurrent()

	c = &Client{
		jack:   jc,
		master: master,
		buf:    sound.NewBufferedSound(master, int(2*jc.BufferSize())),
		aIns:   make(map[string]*gojack.AudioInPort),
		aOuts:  make(map[string]*gojack.AudioOutPort),
		mIns:   make(map[string]*gojack.MidiInPort),
	}

	//~ jc.OnProcess(c.process, nil)
	//~ jc.OnShutdown(c.shutdown, nil)
	err = c.jack.Activate()

	return c, err
}

func (c *Client) Close() {
	c.jack.Close()
}

// PlaySound important: sounds must not use any common Sampleable objects
// because they are sampled concurrently
func (c Client) PlaySound(s dsp.Sound) error {
	c.master.PlaySound(s)
	return nil
}

func (c Client) Play(target chan<- float32) {
	go func(target chan<- float32) {
		for {
			target <- float32(c.master.Sample())
		}
	}(target)
}

// AddAudioIn isTerminal determines whether signal will be passed on or not
// (processed or not)
func (c *Client) AddAudioIn(name string, isTerminal bool) (<-chan float32, error) {
	_, exists := c.aIns[name]
	if exists {
		return nil, fmt.Errorf("port name `%s' already assigned")
	}
	p, err := c.jack.RegisterAudioIn(name, isTerminal)
	c.aIns[name] = p
	return p.Channel(), err
}

// AddAudioOut isSynthesized determines whether signal is generated from scratch
// or processed from a given input signal
func (c *Client) AddAudioOut(name string, isSynthesized bool) (chan<- float32, error) {
	_, exists := c.aOuts[name]
	if exists {
		return nil, fmt.Errorf("port name `%s' already assigned")
	}
	p, err := c.jack.RegisterAudioOut(name, isSynthesized)
	c.aOuts[name] = p
	return p.Channel(), err
}

func (c *Client) AddMidiIn(name string) (<-chan MidiEvent, error) {
	_, exists := c.mIns[name]
	if exists {
		return nil, fmt.Errorf("port name `%s' already assigned", name)
	}
	p, err := c.jack.RegisterMidiIn(name, true)
	c.mIns[name] = p

	chOut := make(chan MidiEvent, c.jack.BufferSize())
	go func(in <-chan gojack.MidiEvent, out chan<- MidiEvent) {
		for {
			s, more := <-in
			if !more {
				return
			}
			out <- MidiEvent(s)
		}
	}(p.Channel(), chOut)

	return chOut, err
}

//~ func (c *Client) addMidiCallback(midiInName string, mc gojack.MidiCallback) error {
//~ p, exists := c.mIns[midiInName]
//~ if !exists {
//~ return fmt.Errorf("no such midi input port: `%s'", midiInName)
//~ }
//~ mc_ := gojack.MidiCallback(mc)
//~ p.AddCallback(&mc_)
//~ return nil
//~ }

//~ func onMidiOnCallback(mc MidiCallback) gojack.MidiCallback {
//~ return func(status byte, i byte, v byte) {
//~ if gojack.MIDI_NOTE_ON == status {
//~ mc(i, v)
//~ }
//~ }
//~ }

//~ func (c *Client) AddMidiOnCallback(midiInName string, mc MidiCallback) error {
//~ return c.addMidiCallback(midiInName, onMidiOnCallback(mc))
//~ }
//~
//~ func onMidiOffCallback(mc MidiCallback) gojack.MidiCallback {
//~ return func(status byte, i byte, v byte) {
//~ if gojack.MIDI_NOTE_OFF == status {
//~ mc(i, v)
//~ }
//~ }
//~ }

//~ func (c *Client) AddMidiOffCallback(midiInName string, mc MidiCallback) error {
//~ return c.addMidiCallback(midiInName, onMidiOffCallback(mc))
//~ }

func getNPorts(ports []*gojack.Port, err error) int {
	if nil != err {
		logError(err.Error())
		return 0
	}
	return len(ports)
}

func (c *Client) NSystemSpeakers() int {
	return getNPorts(c.jack.SystemInputPorts(true))
}

func (c *Client) NSystemAudioSources() int {
	return getNPorts(c.jack.SystemOutputPorts(true))
}

func (c *Client) ConnectSystemSpeaker(name string, systemSpeakerId int) error {
	ports, err := c.jack.SystemInputPorts(true)
	if nil != err {
		return err
	}
	p, ok := c.aOuts[name]
	if !ok {
		return fmt.Errorf("no such output port `%s'", name)
	}
	return c.jack.Connect(&p.Port, ports[systemSpeakerId])
}

func (c *Client) ConnectSystemAudioSource(name string, systemSourceId int) error {
	ports, err := c.jack.SystemOutputPorts(true)
	if nil != err {
		return err
	}
	p, ok := c.aIns[name]
	if !ok {
		return fmt.Errorf("no such input port `%s'", name)
	}
	return c.jack.Connect(ports[systemSourceId], &p.Port)
}

func logError(msg string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, msg+"\n", a...)
}

//~ func (Client) shutdown(interface{}) {
//~ // empty
//~ }

//~ func (c Client) process(in [][]float32, out_ *[][]float32, _ interface{}) error {
//~ out := *out_
//~
//~ nFrames := len(out[0])
//~ for _, m := range c.mIns {
//~ m.ProcessEvents(nFrames)
//~ }
//~
//~ // TODO: ALL THIS WILL BREAK WHEN WE HAVE MORE THAN ONE OUTPUT CHANNEL
//~ for i, buf := range in { // loop input channels
//~ for j, _ := range buf { // loop buffer
//~ out[i][j] = float32(0.2 * c.buf.Sample())
//~ }
//~ }
//~ return nil
//~ }
