import { EventEmitter } from 'node:events';
import { describe, expect, it } from 'vitest';
import { bindProxyRequestLifetime, bindProxyResponseLifetime, bindProxySocketLifetime } from '../../../vite.config';

class FakeRequest extends EventEmitter {
  destroyed = false;

  destroy(): this {
    this.destroyed = true;
    this.emit('close');
    return this;
  }
}

class FakeResponse extends FakeRequest {
  writableEnded = false;
  destroyCount = 0;

  override destroy(): this {
    this.destroyCount++;
    return super.destroy();
  }
}

class FakeIncomingResponse extends EventEmitter {
  complete = false;
}

class FakeSocket extends EventEmitter {
  destroyed = false;

  destroy(): this {
    this.destroyed = true;
    this.emit('close');
    return this;
  }
}

describe('source backend proxy resource lifetime', () => {
  it('destroys a pending upstream HTTP request when the browser response closes', () => {
    const request = new FakeRequest();
    const response = new FakeResponse();

    bindProxyRequestLifetime(request, response);
    response.emit('close');

    expect(request.destroyed).toBe(true);
    expect(request.listenerCount('close')).toBe(0);
    expect(response.listenerCount('close')).toBe(0);
    expect(response.listenerCount('finish')).toBe(0);
  });

  it('keeps a completed HTTP response from destroying its upstream request', () => {
    const request = new FakeRequest();
    const response = new FakeResponse();

    bindProxyRequestLifetime(request, response);
    response.writableEnded = true;
    response.emit('finish');
    response.emit('close');

    expect(request.destroyed).toBe(false);
    expect(request.listenerCount('close')).toBe(0);
  });

  it.each(['error', 'close'])('aborts the browser response when the upstream body ends early (%s)', (event) => {
    const upstream = new FakeIncomingResponse();
    const response = new FakeResponse();

    bindProxyResponseLifetime(upstream, response);
    upstream.emit(event, new Error('incomplete upstream body'));

    expect(response.destroyed).toBe(true);
    if (event !== 'close') upstream.emit('close');
    expect(response.destroyCount).toBe(1);
    expect(upstream.listenerCount('error')).toBe(0);
    expect(upstream.listenerCount('close')).toBe(0);
  });

  it.each(['finished', 'closed'])('leaves an already %s browser response alone during upstream abort', (state) => {
    const upstream = new FakeIncomingResponse();
    const response = new FakeResponse();

    bindProxyResponseLifetime(upstream, response);
    if (state === 'finished') response.writableEnded = true;
    else response.destroy();
    const priorCount = response.destroyCount;

    upstream.emit('error', new Error('incomplete upstream body'));
    upstream.emit('close');

    expect(response.destroyCount).toBe(priorCount);
    expect(upstream.listenerCount('error')).toBe(0);
    expect(upstream.listenerCount('close')).toBe(0);
  });

  it('preserves the browser response when the upstream already completed', () => {
    const upstream = new FakeIncomingResponse();
    const response = new FakeResponse();

    bindProxyResponseLifetime(upstream, response);
    upstream.complete = true;
    upstream.emit('error', new Error('late cleanup failed'));
    upstream.emit('close');

    expect(response.destroyed).toBe(false);
    expect(upstream.listenerCount('error')).toBe(0);
  });

  it('destroys the other WebSocket half and removes listeners when either peer closes', () => {
    const downstream = new FakeSocket();
    const upstream = new FakeSocket();

    bindProxySocketLifetime(downstream, upstream);
    upstream.emit('close');

    expect(downstream.destroyed).toBe(true);
    expect(downstream.listenerCount('close')).toBe(0);
    expect(downstream.listenerCount('error')).toBe(0);
    expect(upstream.listenerCount('close')).toBe(0);
    expect(upstream.listenerCount('error')).toBe(0);
  });
});
