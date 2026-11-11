import {
  decodeLiteratureReadingStatusReceipt,
  type LiteratureReadingStatusCommand,
  type LiteratureReadingStatusReceipt,
} from '@/common/adapter/literatureReadingStatus';
import { literatureValueIdentity } from '@/common/adapter/literatureValueIdentity';
import {
  advanceLiteratureReadingBulk,
  claimLiteratureReadingBulk,
  currentLiteratureReadingBulkCommand,
  decodeLiteratureReadingBulk,
  isLiteratureReadingBulk,
  type LiteratureReadingBulkIntent,
} from './literatureReadingBulkIntent';
import { runLiteratureBulkFlow, type LiteratureBulkCodec, type LiteratureBulkPort } from './literatureBulkFlow';
export { BulkPausedError } from './literatureBulkFlow';

const readingOrigin = (value: LiteratureReadingBulkIntent) => ({
  ...value,
  phase: 'ready',
  confirmed_batches: 0,
  paused: false,
});

export const literatureReadingBulkCodec: LiteratureBulkCodec<
  LiteratureReadingBulkIntent,
  LiteratureReadingStatusCommand,
  LiteratureReadingStatusReceipt
> = {
  is: isLiteratureReadingBulk,
  decode: decodeLiteratureReadingBulk,
  claim: claimLiteratureReadingBulk,
  current: currentLiteratureReadingBulkCommand,
  receipt: decodeLiteratureReadingStatusReceipt,
  advance: advanceLiteratureReadingBulk,
  position: (intent) => intent.confirmed_batches,
  count: (intent) => intent.batches.length,
  sameOrigin: (left, right) => {
    return literatureValueIdentity(readingOrigin(left)) === literatureValueIdentity(readingOrigin(right));
  },
};
export type LiteratureReadingBulkPort = LiteratureBulkPort<
  LiteratureReadingBulkIntent,
  LiteratureReadingStatusCommand,
  LiteratureReadingStatusReceipt
>;
export function runLiteratureReadingBulkFlow(
  initial: LiteratureReadingBulkIntent,
  port: LiteratureReadingBulkPort
): Promise<LiteratureReadingStatusReceipt> {
  return runLiteratureBulkFlow(initial, port, literatureReadingBulkCodec);
}
