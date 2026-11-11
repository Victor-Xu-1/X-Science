import type {
  LiteratureReadingStatus,
  LiteratureReadingStatusCommand,
  LiteratureReadingStatusReceipt,
} from '@/common/adapter/literatureReadingStatus';
import { createLiteratureReadingBulk, type LiteratureReadingBulkIntent } from './literatureReadingBulkIntent';
import { literatureReadingBulkCodec } from './literatureReadingBulkFlow';
import {
  createLiteratureBulkOperations,
  type LiteratureBulkHost,
  type LiteratureBulkSlot,
} from './literatureBulkOperations';

export type LiteratureReadingBulkSlot = LiteratureBulkSlot<LiteratureReadingBulkIntent, LiteratureReadingStatusReceipt>;
export type LiteratureReadingBulkHost<Flight> = LiteratureBulkHost<
  Flight,
  LiteratureReadingBulkIntent,
  LiteratureReadingStatusCommand,
  LiteratureReadingStatusReceipt
>;
/** The reading adapter preserves its v1 records and 200-ID command contract. */
export function createLiteratureReadingBulkOperations<Flight>(host: LiteratureReadingBulkHost<Flight>) {
  const operations = createLiteratureBulkOperations(host, literatureReadingBulkCodec);
  return {
    ...operations,
    start(ids: readonly string[], status: LiteratureReadingStatus, scopeKey: string, canContinue?: () => boolean) {
      return operations.start(createLiteratureReadingBulk(ids, status, scopeKey, host.uuid), canContinue);
    },
  };
}
