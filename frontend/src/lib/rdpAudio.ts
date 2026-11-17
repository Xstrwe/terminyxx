/**
 * Plays the remote desktop's sound (IronRDP's audioPlayback extension, see
 * frontend/vendor/ironrdp/README.md) through Web Audio. Waves arrive as
 * interleaved 16-bit little-endian PCM and are scheduled back to back on the
 * AudioContext clock, so consecutive chunks play without gaps.
 */

type AudioMessage =
  | { type: 'wave'; sampleRate: number; channels: number; data: Uint8Array }
  | { type: 'volume'; left: number; right: number }
  | { type: 'close' };

/** Played ahead of the clock to absorb network jitter. */
const LEAD = 0.08;
/** Queues longer than this are dropped so sound can't lag far behind the picture. */
const MAX_QUEUE = 0.5;

export class RdpAudioPlayer {
  private ctx: AudioContext | null = null;
  private gain: GainNode | null = null;
  private next = 0;
  private closed = false;

  /** The callback to hand to `audioPlayback(...)`. */
  readonly onMessage = (message: unknown) => {
    if (this.closed || !message || typeof message !== 'object') return;
    const m = message as AudioMessage;
    switch (m.type) {
      case 'wave':
        this.play(m.sampleRate, m.channels, m.data);
        break;
      case 'volume':
        this.setVolume(m.left, m.right);
        break;
      case 'close':
        // Format (re)negotiation: start the next wave fresh.
        this.next = 0;
        break;
    }
  };

  private context(): AudioContext {
    if (!this.ctx) {
      this.ctx = new AudioContext({ latencyHint: 'interactive' });
      this.gain = this.ctx.createGain();
      this.gain.connect(this.ctx.destination);
    }
    // Autoplay policies start a context suspended until a user gesture;
    // connecting was one, and every later wave retries.
    if (this.ctx.state === 'suspended') void this.ctx.resume().catch(() => {});
    return this.ctx;
  }

  private play(sampleRate: number, channels: number, data: Uint8Array) {
    if (!sampleRate || !channels || data.byteLength < 2 * channels) return;
    const ctx = this.context();
    const frames = Math.floor(data.byteLength / (2 * channels));
    const buffer = ctx.createBuffer(channels, frames, sampleRate);
    const view = new DataView(data.buffer, data.byteOffset, frames * 2 * channels);
    for (let ch = 0; ch < channels; ch++) {
      const out = buffer.getChannelData(ch);
      for (let i = 0; i < frames; i++) out[i] = view.getInt16((i * channels + ch) * 2, true) / 32768;
    }
    const now = ctx.currentTime;
    if (this.next < now + 0.005 || this.next > now + MAX_QUEUE) this.next = now + LEAD;
    const src = ctx.createBufferSource();
    src.buffer = buffer;
    src.connect(this.gain!);
    src.start(this.next);
    this.next += buffer.duration;
  }

  private setVolume(left: number, right: number) {
    this.context();
    // The protocol level is already on a logarithmic (perceptual) scale, so
    // using it as the gain directly keeps the remote volume slider's feel.
    this.gain!.gain.value = Math.max(0, Math.min(1, (left + right) / 2));
  }

  /** Resumes a context the autoplay policy left suspended (call on a click). */
  resume() {
    if (this.ctx?.state === 'suspended') void this.ctx.resume().catch(() => {});
  }

  close() {
    this.closed = true;
    void this.ctx?.close().catch(() => {});
    this.ctx = null;
    this.gain = null;
  }
}
