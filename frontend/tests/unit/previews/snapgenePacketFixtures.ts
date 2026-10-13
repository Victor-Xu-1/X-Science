// Synthetic native packets: exact cookie, arbitrary bases, and bounded framing controls.
export type PacketFixture = {
  id: string;
  hex: string;
  outcome: 'sequence' | 'empty-content' | 'parse-failed' | 'parser-error';
};

const cookie = '090000000e536e617047656e65000100000000';
const dna = '0000000009004143475454474341';

export const packetFixtures: PacketFixture[] = [
  { id: 'valid-basic', hex: '090000000e536e617047656e650001000000000000000009004143475454474341', outcome: 'sequence' },
  {
    id: 'valid-notes-and-binary-aux',
    hex: '090000000e536e617047656e6500010000000006000000453c4e6f7465733e3c436f6d6d656e743e4c4f43555320616e64204f524947494e20617265206861726d6c657373207465726d733c2f436f6d6d656e743e3c2f4e6f7465733e00000000090041434754544743410200000003fffe80',
    outcome: 'sequence',
  },
  {
    id: 'valid-unknown-aux-before',
    hex: '090000000e536e617047656e65000100000000fe00000004ff0080fe0000000009004143475454474341',
    outcome: 'sequence',
  },
  {
    id: 'valid-unknown-aux-after',
    hex: '090000000e536e617047656e650001000000000000000009004143475454474341fe00000004ff0080fe',
    outcome: 'sequence',
  },
  {
    id: 'retained-lead-116',
    hex: '090000000e536e617047656e650001000000000006000000453c4e6f7465733e3c436f6d6d656e743e4c4f43555320616e64204f524947494e20617265206861726d6c657373207465726d733c2f436f6d6d656e743e3c2f4e6f7465733e00000000090141434754544743410200000003fffe80',
    outcome: 'parse-failed',
  },
  {
    id: 'truncated-dna-payload',
    hex: '090000000e536e617047656e6500010000000000000000090041434754',
    outcome: 'parse-failed',
  },
  {
    id: 'truncated-unknown-aux',
    hex: '090000000e536e617047656e650001000000000000000009004143475454474341fe00000008ff80',
    outcome: 'parse-failed',
  },
  {
    id: 'truncated-notes-payload',
    hex: '090000000e536e617047656e65000100000000000000000900414347545447434106000000493c4e6f7465733e3c436f6d6d656e743e4c4f43555320616e64204f524947494e20617265206861726d6c657373207465726d733c2f436f6d6d656e743e3c2f4e6f7465733e',
    outcome: 'parse-failed',
  },
  {
    id: 'truncated-packet-header-1',
    hex: '090000000e536e617047656e65000100000000000000000900414347545447434100',
    outcome: 'parse-failed',
  },
  {
    id: 'truncated-packet-header-4',
    hex: '090000000e536e617047656e65000100000000000000000900414347545447434100000000',
    outcome: 'parse-failed',
  },
  { id: 'zero-length-dna-packet', hex: '090000000e536e617047656e650001000000000000000000', outcome: 'parse-failed' },
  { id: 'header-only-empty', hex: '090000000e536e617047656e65000100000000', outcome: 'empty-content' },
  {
    id: 'invalid-cookie',
    hex: '090000000e586e617047656e650001000000000000000009004143475454474341',
    outcome: 'parse-failed',
  },
  { id: 'empty-unknown-before-dna', hex: cookie + 'fe00000000' + dna, outcome: 'sequence' },
  { id: 'empty-unknown-after-dna', hex: cookie + dna + 'fe00000000', outcome: 'sequence' },
  { id: 'complete-unknown-no-dna', hex: cookie + 'fe00000002ff80', outcome: 'empty-content' },
  { id: 'complete-notes-no-dna', hex: cookie + packetHex(6, '<Notes/>'), outcome: 'empty-content' },
  { id: 'flag-only-dna', hex: cookie + '000000000100', outcome: 'empty-content' },
  { id: 'zero-length-dna-after-dna', hex: cookie + dna + '0000000000', outcome: 'parse-failed' },
  { id: 'truncated-unknown-no-dna', hex: cookie + 'fe00000008ff80', outcome: 'parse-failed' },
  { id: 'truncated-notes-no-dna', hex: cookie + '06000000084143', outcome: 'parse-failed' },
  { id: 'malformed-features-no-dna', hex: cookie + packetHex(10, '<Features/>'), outcome: 'parser-error' },
  { id: 'malformed-features-after-dna', hex: cookie + dna + packetHex(10, '<Features/>'), outcome: 'parser-error' },
  ...[1, 2, 3, 4].flatMap((size): PacketFixture[] => [
    { id: `short-header-${size}-no-dna`, hex: cookie + '00'.repeat(size), outcome: 'parse-failed' },
    { id: `short-header-${size}-after-dna`, hex: cookie + dna + '00'.repeat(size), outcome: 'parse-failed' },
  ]),
  ...['80000000', 'ffffffff'].flatMap((length): PacketFixture[] => [
    { id: `unsigned-dna-length-${length}`, hex: cookie + '00' + length + '0041434754', outcome: 'parse-failed' },
    { id: `unsigned-unknown-length-${length}`, hex: cookie + dna + 'fe' + length + 'ff80', outcome: 'parse-failed' },
  ]),
];

function packetHex(type: number, payload: string): string {
  const bytes = Buffer.from(payload);
  const header = Buffer.alloc(5);
  header[0] = type;
  header.writeUInt32BE(bytes.byteLength, 1);
  return Buffer.concat([header, bytes]).toString('hex');
}

export function fixtureBytes(fixture: PacketFixture): Buffer {
  return Buffer.from(fixture.hex, 'hex');
}
