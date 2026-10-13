/** Closed presentation of an existing pause reason, never a lifecycle decision. */
export function runtimePausePresentation(reason: string | null): { title: string; description: string } {
  switch (reason) {
    case 'provider_stream_no_progress':
      return { title: 'modelResponsePaused', description: 'modelNoProgressPaused' };
    case 'provider_output_token_limit':
      return { title: 'taskPaused', description: 'outputLimitPaused' };
    default:
      return { title: 'taskPaused', description: 'recoveryPaused' };
  }
}
