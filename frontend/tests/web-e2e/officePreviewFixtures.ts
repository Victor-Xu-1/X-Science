import { Buffer } from 'node:buffer';
import { randomUUID } from 'node:crypto';
import { crc32 } from 'node:zlib';
import { Document, Packer, Paragraph, Table, TableCell, TableRow } from 'docx';
import * as XLSX from 'xlsx-republish';
import { seedSynonGoOfficeArtifact } from './synonGoFrameFixture';

export type OfficePreviewFixture = {
  kind: 'pdf' | 'docx' | 'xlsx' | 'pptx';
  filename: string;
  contentType: string;
  source: Uint8Array;
};

/** Small real files, generated without network access or an office installation. */
export async function createOfficePreviewFixtures(): Promise<OfficePreviewFixture[]> {
  const doc = new Document({
    sections: [
      {
        children: [
          new Paragraph('PREVIEW TEST / 预览测试'),
          new Paragraph('Office preview preserves Chinese text: 中文内容。'),
          new Table({
            rows: [
              ['Item', 'Value'],
              ['Alpha', '10'],
              ['Beta', '20'],
            ].map(
              (row) => new TableRow({ children: row.map((text) => new TableCell({ children: [new Paragraph(text)] })) })
            ),
          }),
        ],
      },
    ],
  });
  const workbook = XLSX.utils.book_new();
  // A deliberately absent row must remain row 2, not move A3 into A2.
  XLSX.utils.book_append_sheet(
    workbook,
    {
      A1: { t: 's', v: 'PREVIEW TEST / 预览测试' },
      A3: { t: 's', v: 'THIRD ROW / 第三行' },
      B3: { t: 'n', v: 30 },
      '!ref': 'A1:B3',
    },
    'Sparse'
  );
  XLSX.utils.book_append_sheet(
    workbook,
    {
      A1: { t: 's', v: 'Amount / 数值' },
      A2: { t: 'n', v: 10 },
      A3: { t: 'n', v: 20 },
      A4: { t: 'n', f: 'SUM(A2:A3)', v: 30 },
      '!ref': 'A1:A4',
    },
    'Totals'
  );
  return [
    { kind: 'pdf', filename: 'preview-test.pdf', contentType: 'application/pdf', source: createPreviewPdf() },
    {
      kind: 'docx',
      filename: 'preview-test.docx',
      contentType: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
      source: await Packer.toBuffer(doc),
    },
    {
      kind: 'xlsx',
      filename: 'preview-test.xlsx',
      contentType: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
      source: XLSX.write(workbook, { type: 'buffer', bookType: 'xlsx' }) as Buffer,
    },
    {
      kind: 'pptx',
      filename: 'preview-test.pptx',
      contentType: 'application/vnd.openxmlformats-officedocument.presentationml.presentation',
      source: await createPreviewPresentation(),
    },
  ];
}

export function createPreviewPdf(marker = 'Actual PDF text and graphic'): Uint8Array {
  if (!/^[A-Za-z0-9 ]{1,80}$/.test(marker)) throw new Error('PDF fixture marker must be plain ASCII text');
  const streams = [1, 2].map(
    (page) =>
      `BT /F1 18 Tf 40 240 Td (PREVIEW TEST page ${page}) Tj 0 -30 Td /F1 12 Tf (${marker}) Tj 0 -24 Td /F2 14 Tf <988489C86D4B8BD5> Tj ET\n0.1 0.5 0.5 rg 40 70 100 60 re f\n`
  );
  const objects = [
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 2 >>',
    ...[5, 6].map(
      (stream) =>
        `<< /Type /Page /Parent 2 0 R /MediaBox [0 0 420 300] /Resources << /Font << /F1 7 0 R /F2 8 0 R >> >> /Contents ${stream} 0 R >>`
    ),
    ...streams.map((stream) => `<< /Length ${Buffer.byteLength(stream)} >>\nstream\n${stream}endstream`),
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',
    '<< /Type /Font /Subtype /Type0 /BaseFont /STSong-Light /Encoding /UniGB-UCS2-H /DescendantFonts [9 0 R] >>',
    '<< /Type /Font /Subtype /CIDFontType0 /BaseFont /STSong-Light /CIDSystemInfo << /Registry (Adobe) /Ordering (GB1) /Supplement 4 >> /FontDescriptor 10 0 R /DW 1000 >>',
    '<< /Type /FontDescriptor /FontName /STSong-Light /Flags 6 /FontBBox [-25 -254 1000 880] /ItalicAngle 0 /Ascent 880 /Descent -120 /CapHeight 880 /StemV 80 >>',
  ];
  let pdf = '%PDF-1.4\n';
  const offsets = objects.map((object, index) => {
    const offset = Buffer.byteLength(pdf);
    pdf += `${index + 1} 0 obj\n${object}\nendobj\n`;
    return offset;
  });
  const xref = Buffer.byteLength(pdf);
  pdf += `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n`;
  pdf += offsets.map((offset) => `${String(offset).padStart(10, '0')} 00000 n \n`).join('');
  pdf += `trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`;
  return Buffer.from(pdf);
}

/** Seed immutable versions through the existing Go test fixture and Store. */
export function seedEncodedOfficeArtifact(frameId: string) {
  return seedSynonGoOfficeArtifact({
    frameId,
    officeArtifact: {
      artifactId: `office artifact/${randomUUID()} 中文`,
      versions: ['EXACT VERSION ONE', 'LATEST VERSION TWO'].map((marker) =>
        Buffer.from(createPreviewPdf(marker)).toString('base64')
      ),
    },
  });
}

async function createPreviewPresentation(): Promise<Uint8Array> {
  const zip = new Map<string, string>();
  const drawing = 'http://schemas.openxmlformats.org/drawingml/2006/main';
  const presentation = 'http://schemas.openxmlformats.org/presentationml/2006/main';
  const relationships = 'http://schemas.openxmlformats.org/officeDocument/2006/relationships';
  const packageRelationships = 'http://schemas.openxmlformats.org/package/2006/relationships';
  zip.set(
    '[Content_Types].xml',
    `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/>${[1, 2].map((page) => `<Override PartName="/ppt/slides/slide${page}.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/>`).join('')}</Types>`
  );
  zip.set(
    '_rels/.rels',
    `<Relationships xmlns="${packageRelationships}"><Relationship Id="rId1" Type="${relationships}/officeDocument" Target="ppt/presentation.xml"/></Relationships>`
  );
  zip.set(
    'ppt/presentation.xml',
    `<p:presentation xmlns:p="${presentation}" xmlns:r="${relationships}"><p:sldIdLst><p:sldId id="256" r:id="rId1"/><p:sldId id="257" r:id="rId2"/></p:sldIdLst><p:sldSz cx="12192000" cy="6858000"/><p:notesSz cx="6858000" cy="9144000"/></p:presentation>`
  );
  zip.set(
    'ppt/_rels/presentation.xml.rels',
    `<Relationships xmlns="${packageRelationships}">${[1, 2].map((page) => `<Relationship Id="rId${page}" Type="${relationships}/slide" Target="slides/slide${page}.xml"/>`).join('')}</Relationships>`
  );
  for (const page of [1, 2]) {
    zip.set(
      `ppt/slides/slide${page}.xml`,
      `<p:sld xmlns:p="${presentation}" xmlns:a="${drawing}"><p:cSld><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr><p:sp><p:nvSpPr><p:cNvPr id="2" name="Preview title"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr><p:spPr><a:xfrm><a:off x="400000" y="400000"/><a:ext cx="11000000" cy="4000000"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr><p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:rPr lang="en-US" sz="2400"/><a:t>PREVIEW TEST slide ${page}</a:t></a:r></a:p><a:p><a:r><a:rPr lang="zh-CN" sz="1600"/><a:t>预览测试 / Chinese content ${page}</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sld>`
    );
  }
  return storedZip(zip);
}

/** STORE-only ZIP writer for these tiny generated OOXML parts; no ZIP dependency. */
function storedZip(entries: ReadonlyMap<string, string>): Uint8Array {
  const files: Buffer[] = [];
  const directory: Buffer[] = [];
  let offset = 0;
  for (const [name, value] of entries) {
    const filename = Buffer.from(name);
    const content = Buffer.from(value);
    const checksum = crc32(content);
    const local = Buffer.alloc(30);
    local.writeUInt32LE(0x04034b50);
    local.writeUInt16LE(20, 4);
    local.writeUInt16LE(0x0800, 6);
    local.writeUInt16LE(33, 12);
    local.writeUInt32LE(checksum, 14);
    local.writeUInt32LE(content.length, 18);
    local.writeUInt32LE(content.length, 22);
    local.writeUInt16LE(filename.length, 26);
    files.push(local, filename, content);
    const central = Buffer.alloc(46);
    central.writeUInt32LE(0x02014b50);
    central.writeUInt16LE(20, 4);
    local.copy(central, 6, 4, 30);
    central.writeUInt32LE(offset, 42);
    directory.push(central, filename);
    offset += local.length + filename.length + content.length;
  }
  const central = Buffer.concat(directory);
  const end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50);
  end.writeUInt16LE(entries.size, 8);
  end.writeUInt16LE(entries.size, 10);
  end.writeUInt32LE(central.length, 12);
  end.writeUInt32LE(offset, 16);
  return Buffer.concat([...files, central, end]);
}
