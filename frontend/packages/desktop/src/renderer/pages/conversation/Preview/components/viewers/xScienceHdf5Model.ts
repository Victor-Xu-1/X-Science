export type Hdf5NodeKind = 'group' | 'dataset' | 'datatype' | 'link';

export type Hdf5AttributeSummary = {
  name: string;
  value: string;
};

export type Hdf5TreeNode = {
  name: string;
  path: string;
  kind: Hdf5NodeKind;
  shape?: number[] | null;
  dtype?: string;
  attributes: Hdf5AttributeSummary[];
  children?: Hdf5TreeNode[];
};

export type Hdf5DatasetDetail = {
  path: string;
  shape: number[] | null;
  dtype: string;
  preview: unknown;
  truncated: boolean;
};

export type AnnDataDistributionItem = {
  label: string;
  labelKey?: 'unlabeled' | 'other';
  count: number;
};

export type AnnDataDistribution = {
  key: string;
  label: string;
  labelKey?: 'cellType' | 'responder' | 'response' | 'recist' | 'pathology' | 'sex';
  total: number;
  items: AnnDataDistributionItem[];
};

export type AnnDataEmbedding = {
  key: string;
  label: string;
  labelKey?: 'pcaPreview';
  source: 'stored' | 'derived';
  totalPoints: number;
  sampledPoints: number;
  points: [number, number][];
};

export type AnnDataOverview = {
  cells: number;
  features: number;
  encodingVersion?: string;
  distributions: AnnDataDistribution[];
  embedding?: AnnDataEmbedding;
};

export type Hdf5WorkerRequest =
  | { type: 'open'; filename: string; buffer: ArrayBuffer }
  | { type: 'read'; path: string; requestId: number };

export type Hdf5WorkerErrorCode = 'not-ready' | 'not-dataset' | 'parse-failed' | 'read-failed';

export type Hdf5WorkerResponse =
  | { type: 'ready'; root: Hdf5TreeNode; nodeCount: number; truncated: boolean; overview?: AnnDataOverview }
  | { type: 'detail'; requestId: number; detail: Hdf5DatasetDetail }
  | {
      type: 'error';
      requestId?: number;
      code: Hdf5WorkerErrorCode;
    };

// Display normalized HDF5 values as JSON-like text, retaining numeric NaN and
// infinities as unquoted tokens. Strings (including those spellings and exact
// int64 decimals) keep JSON quoting. This never changes the transported values.
export function formatHdf5DisplayValue(value: unknown, indent = 0): string | undefined {
  const format = (item: unknown, depth: number): string | undefined => {
    if (typeof item === 'number' && !Number.isFinite(item)) return String(item);
    if (!item || typeof item !== 'object') return JSON.stringify(item);

    const array = Array.isArray(item);
    const entries = array
      ? Array.from(item, (element) => format(element, depth + 1) ?? 'null')
      : Object.entries(item).flatMap(([key, element]) => {
          const text = format(element, depth + 1);
          return text === undefined ? [] : [`${JSON.stringify(key)}:${indent ? ' ' : ''}${text}`];
        });
    const [open, close] = array ? ['[', ']'] : ['{', '}'];
    if (entries.length === 0) return `${open}${close}`;
    if (!indent) return `${open}${entries.join(',')}${close}`;
    const padding = ' '.repeat(indent * (depth + 1));
    return `${open}\n${padding}${entries.join(`,\n${padding}`)}\n${' '.repeat(indent * depth)}${close}`;
  };
  return format(value, 0);
}
