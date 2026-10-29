/** A local preparation was withdrawn before transport, never an admitted execution failure. */
export class ConversationPreparationCancelledError extends Error {
  constructor() {
    super('conversation_preparation_cancelled');
    this.name = 'ConversationPreparationCancelledError';
  }
}
