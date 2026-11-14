// QR Code encoder (ISO/IEC 18004): byte mode, error correction level M, the smallest version that fits and the
// mask with the lowest penalty. Written here so the TOTP secret never leaves the browser and no dependency is added.

export type QrSymbol = {
  version: number;
  mask: number;
  /** modules[row][column], true = dark. No quiet zone. */
  modules: boolean[][];
};

// Error correction level M, indexed by version (index 0 unused).
const EC_CODEWORDS_PER_BLOCK = [
  -1, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26, 30, 22, 22, 24, 24, 28, 28, 26, 26, 26, 26, 28, 28, 28, 28, 28, 28, 28,
  28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28,
];
const EC_BLOCKS = [
  -1, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5, 5, 8, 9, 9, 10, 10, 11, 13, 14, 16, 17, 17, 18, 20, 21, 23, 25, 26, 28, 29, 31, 33,
  35, 37, 38, 40, 43, 45, 47, 49,
];
const FORMAT_EC_LEVEL_M = 0b00;
const BYTE_MODE = 0b0100;

/** Encodes text as UTF-8 bytes in a level-M QR symbol; throws when it does not fit in version 40. */
export function encodeQr(text: string): QrSymbol {
  const data = new TextEncoder().encode(text);
  let version = 1;
  while (dataCapacityBits(version) < 4 + countBits(version) + data.length * 8) {
    if (version === 40) {
      throw new RangeError(
        `QR: the text takes ${data.length} bytes and version 40-M holds at most ${maxBytes()} bytes in byte mode`,
      );
    }
    version++;
  }

  const symbol = new SymbolBuilder(version);
  symbol.placeCodewords(addErrorCorrection(dataCodewords(data, version), version));
  let mask = 0;
  let best = Infinity;
  for (let m = 0; m < 8; m++) {
    symbol.applyMask(m);
    symbol.drawFormatBits(m);
    const penalty = symbol.penalty();
    if (penalty < best) {
      best = penalty;
      mask = m;
    }
    symbol.applyMask(m); // XOR again to undo
  }
  symbol.applyMask(mask);
  symbol.drawFormatBits(mask);
  return { version, mask, modules: symbol.modules };
}

function countBits(version: number): number {
  return version <= 9 ? 8 : 16;
}

function rawCodewords(version: number): number {
  let modules = (16 * version + 128) * version + 64;
  if (version >= 2) {
    const align = Math.floor(version / 7) + 2;
    modules -= (25 * align - 10) * align - 55;
    if (version >= 7) modules -= 36;
  }
  return Math.floor(modules / 8);
}

function dataCapacityBits(version: number): number {
  return (rawCodewords(version) - EC_CODEWORDS_PER_BLOCK[version]! * EC_BLOCKS[version]!) * 8;
}

function maxBytes(): number {
  return Math.floor((dataCapacityBits(40) - 4 - countBits(40)) / 8);
}

function dataCodewords(data: Uint8Array, version: number): number[] {
  const bits: number[] = [];
  const push = (value: number, length: number) => {
    for (let i = length - 1; i >= 0; i--) bits.push((value >>> i) & 1);
  };
  push(BYTE_MODE, 4);
  push(data.length, countBits(version));
  for (const b of data) push(b, 8);
  const capacity = dataCapacityBits(version);
  push(0, Math.min(4, capacity - bits.length)); // terminator
  push(0, (8 - (bits.length % 8)) % 8);
  const codewords: number[] = [];
  for (let i = 0; i < bits.length; i += 8) codewords.push(bits.slice(i, i + 8).reduce((acc, bit) => (acc << 1) | bit, 0));
  for (let pad = 0xec; codewords.length < capacity / 8; pad ^= 0xec ^ 0x11) codewords.push(pad);
  return codewords;
}

/** Splits the data into blocks, appends Reed-Solomon codewords and interleaves them. */
function addErrorCorrection(data: number[], version: number): number[] {
  const blocks = EC_BLOCKS[version]!;
  const ecLength = EC_CODEWORDS_PER_BLOCK[version]!;
  const raw = rawCodewords(version);
  const shortBlocks = blocks - (raw % blocks);
  const shortDataLength = Math.floor(raw / blocks) - ecLength;
  const divisor = rsDivisor(ecLength);
  const dataBlocks: number[][] = [];
  const ecBlocks: number[][] = [];
  for (let i = 0, offset = 0; i < blocks; i++) {
    const length = shortDataLength + (i < shortBlocks ? 0 : 1);
    const block = data.slice(offset, offset + length);
    offset += length;
    dataBlocks.push(block);
    ecBlocks.push(rsRemainder(block, divisor));
  }
  const result: number[] = [];
  for (let i = 0; i <= shortDataLength; i++) {
    for (const block of dataBlocks) if (i < block.length) result.push(block[i]!);
  }
  for (let i = 0; i < ecLength; i++) for (const block of ecBlocks) result.push(block[i]!);
  return result;
}

/** Multiplies in GF(2^8) with the QR polynomial x^8 + x^4 + x^3 + x^2 + 1. */
function gfMultiply(x: number, y: number): number {
  let z = 0;
  for (let i = 7; i >= 0; i--) {
    z = (z << 1) ^ ((z >>> 7) * 0x11d);
    z ^= ((y >>> i) & 1) * x;
  }
  return z;
}

function rsDivisor(degree: number): number[] {
  const result = new Array<number>(degree).fill(0);
  result[degree - 1] = 1;
  let root = 1;
  for (let i = 0; i < degree; i++) {
    for (let j = 0; j < degree; j++) {
      result[j] = gfMultiply(result[j]!, root);
      if (j + 1 < degree) result[j]! ^= result[j + 1]!;
    }
    root = gfMultiply(root, 0x02);
  }
  return result;
}

function rsRemainder(data: number[], divisor: number[]): number[] {
  const result = new Array<number>(divisor.length).fill(0);
  for (const b of data) {
    const factor = b ^ result.shift()!;
    result.push(0);
    divisor.forEach((coefficient, i) => (result[i]! ^= gfMultiply(coefficient, factor)));
  }
  return result;
}

function maskBit(mask: number, row: number, col: number): boolean {
  switch (mask) {
    case 0:
      return (row + col) % 2 === 0;
    case 1:
      return row % 2 === 0;
    case 2:
      return col % 3 === 0;
    case 3:
      return (row + col) % 3 === 0;
    case 4:
      return (Math.floor(row / 2) + Math.floor(col / 3)) % 2 === 0;
    case 5:
      return ((row * col) % 2) + ((row * col) % 3) === 0;
    case 6:
      return (((row * col) % 2) + ((row * col) % 3)) % 2 === 0;
    default:
      return (((row + col) % 2) + ((row * col) % 3)) % 2 === 0;
  }
}

class SymbolBuilder {
  readonly size: number;
  readonly modules: boolean[][];
  private readonly reserved: boolean[][];

  constructor(readonly version: number) {
    this.size = version * 4 + 17;
    this.modules = Array.from({ length: this.size }, () => new Array<boolean>(this.size).fill(false));
    this.reserved = Array.from({ length: this.size }, () => new Array<boolean>(this.size).fill(false));
    this.drawFunctionPatterns();
  }

  private set(row: number, col: number, dark: boolean) {
    this.modules[row]![col] = dark;
    this.reserved[row]![col] = true;
  }

  private drawFunctionPatterns() {
    const { size } = this;
    for (let i = 0; i < size; i++) {
      this.set(6, i, i % 2 === 0);
      this.set(i, 6, i % 2 === 0);
    }
    for (const [row, col] of [
      [3, 3],
      [3, size - 4],
      [size - 4, 3],
    ] as const) {
      for (let dr = -4; dr <= 4; dr++) {
        for (let dc = -4; dc <= 4; dc++) {
          const r = row + dr;
          const c = col + dc;
          const distance = Math.max(Math.abs(dr), Math.abs(dc));
          if (r >= 0 && r < size && c >= 0 && c < size) this.set(r, c, distance !== 2 && distance !== 4);
        }
      }
    }
    const centers = this.alignmentCenters();
    const last = centers.length - 1;
    centers.forEach((row, i) =>
      centers.forEach((col, j) => {
        if ((i === 0 && j === 0) || (i === 0 && j === last) || (i === last && j === 0)) return; // finder corners
        for (let dr = -2; dr <= 2; dr++) {
          for (let dc = -2; dc <= 2; dc++) this.set(row + dr, col + dc, Math.max(Math.abs(dr), Math.abs(dc)) !== 1);
        }
      }),
    );
    this.drawFormatBits(0); // reserves the area; the real bits are drawn after masking
    this.drawVersionBits();
  }

  private alignmentCenters(): number[] {
    if (this.version === 1) return [];
    const count = Math.floor(this.version / 7) + 2;
    const step = Math.floor((this.version * 8 + count * 3 + 5) / (count * 4 - 4)) * 2;
    const result = [6];
    for (let pos = this.size - 7; result.length < count; pos -= step) result.splice(1, 0, pos);
    return result;
  }

  drawFormatBits(mask: number) {
    const data = (FORMAT_EC_LEVEL_M << 3) | mask;
    let rem = data;
    for (let i = 0; i < 10; i++) rem = (rem << 1) ^ ((rem >>> 9) * 0x537);
    const bits = ((data << 10) | rem) ^ 0x5412;
    const bit = (i: number) => ((bits >>> i) & 1) !== 0;
    const { size } = this;
    for (let i = 0; i <= 5; i++) this.set(i, 8, bit(i));
    this.set(7, 8, bit(6));
    this.set(8, 8, bit(7));
    this.set(8, 7, bit(8));
    for (let i = 9; i < 15; i++) this.set(8, 14 - i, bit(i));
    for (let i = 0; i < 8; i++) this.set(8, size - 1 - i, bit(i));
    for (let i = 8; i < 15; i++) this.set(size - 15 + i, 8, bit(i));
    this.set(size - 8, 8, true); // dark module
  }

  private drawVersionBits() {
    if (this.version < 7) return;
    let rem = this.version;
    for (let i = 0; i < 12; i++) rem = (rem << 1) ^ ((rem >>> 11) * 0x1f25);
    const bits = (this.version << 12) | rem;
    for (let i = 0; i < 18; i++) {
      const dark = ((bits >>> i) & 1) !== 0;
      const a = this.size - 11 + (i % 3);
      const b = Math.floor(i / 3);
      this.set(b, a, dark);
      this.set(a, b, dark);
    }
  }

  /** Places the codewords in the two-column zigzag, skipping function modules; remainder bits stay light. */
  placeCodewords(codewords: number[]) {
    const { size } = this;
    let i = 0;
    for (let right = size - 1; right >= 1; right -= 2) {
      if (right === 6) right = 5; // skip the vertical timing pattern
      for (let vert = 0; vert < size; vert++) {
        for (let j = 0; j < 2; j++) {
          const col = right - j;
          const upward = ((right + 1) & 2) === 0;
          const row = upward ? size - 1 - vert : vert;
          if (!this.reserved[row]![col] && i < codewords.length * 8) {
            this.modules[row]![col] = ((codewords[i >>> 3]! >>> (7 - (i & 7))) & 1) !== 0;
            i++;
          }
        }
      }
    }
  }

  applyMask(mask: number) {
    for (let row = 0; row < this.size; row++) {
      for (let col = 0; col < this.size; col++) {
        if (!this.reserved[row]![col] && maskBit(mask, row, col)) this.modules[row]![col] = !this.modules[row]![col];
      }
    }
  }

  /** ISO/IEC 18004 penalty: runs (N1 = 3), 2x2 blocks (N2 = 3), finder-like patterns (N3 = 40), balance (N4 = 10). */
  penalty(): number {
    const { size, modules } = this;
    const at = (row: number, col: number, transpose: boolean) => (transpose ? modules[col]![row]! : modules[row]![col]!);
    let points = 0;
    for (const transpose of [false, true]) {
      for (let row = 0; row < size; row++) {
        let run = 0;
        let window = 0;
        for (let col = 0; col < size; col++) {
          const dark = at(row, col, transpose);
          run = col > 0 && dark === at(row, col - 1, transpose) ? run + 1 : 1;
          if (run === 5) points += 3;
          else if (run > 5) points += 1;
          window = ((window << 1) & 0x7ff) | (dark ? 1 : 0);
          if (col >= 10 && (window === 0b10111010000 || window === 0b00001011101)) points += 40;
        }
      }
    }
    let dark = 0;
    for (let row = 0; row < size; row++) {
      for (let col = 0; col < size; col++) {
        if (modules[row]![col]) dark++;
        if (row < size - 1 && col < size - 1) {
          const color = modules[row]![col];
          if (color === modules[row]![col + 1] && color === modules[row + 1]![col] && color === modules[row + 1]![col + 1]) {
            points += 3;
          }
        }
      }
    }
    points += Math.floor(Math.abs((dark * 100) / (size * size) - 50) / 5) * 10;
    return points;
  }
}
