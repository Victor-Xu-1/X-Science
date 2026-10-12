import {
  isPendingXScienceAskUserHistory,
  normalizeXScienceAskUserHistory,
  normalizeXScienceAskUserToolInput,
} from '@/renderer/components/xScience/runtime/askUserHistoryModel';
import { describe, expect, it } from 'vitest';

describe('ask user history model', () => {
  it('identifies only unresolved ask-user history placeholders as pending', () => {
    const input = {
      header: 'Detector',
      question: 'Which detector?',
      options: [{ label: 'UV', description: 'UV' }],
    };
    expect(isPendingXScienceAskUserHistory(input, undefined)).toBe(true);
    expect(
      isPendingXScienceAskUserHistory(input, JSON.stringify({ version: 1, status: 'awaiting_user_response' }))
    ).toBe(true);
    expect(
      isPendingXScienceAskUserHistory(
        input,
        JSON.stringify({
          version: 1,
          status: 'answered',
          action: 'answer',
          answers: { 'Which detector?': 'UV' },
        })
      )
    ).toBe(false);
  });

  it('normalizes a v1.1 ask_user input and answered result', () => {
    const question = normalizeXScienceAskUserToolInput({
      header: 'Assay endpoint',
      question: 'Which endpoint should be primary?',
      options: [{ label: 'pIC50', metadata: { smiles: 'CCO' } }],
      multi_select: false,
    });
    expect(question).toMatchObject({
      header: 'Assay endpoint',
      question: 'Which endpoint should be primary?',
      options: [{ label: 'pIC50', smiles: 'CCO' }],
    });
    expect(
      normalizeXScienceAskUserHistory(
        question!.question,
        JSON.stringify({ version: 1, status: 'answered', action: 'answer', answers: { [question!.question]: 'pIC50' } })
      )
    ).toEqual({ status: 'answered', answer: 'pIC50' });
  });

  it('preserves all v1.1 terminal states', () => {
    expect(
      normalizeXScienceAskUserHistory('Question', JSON.stringify({ version: 1, status: 'awaiting_user_response' }))
    ).toEqual({ status: 'pending', answer: null });
    expect(
      normalizeXScienceAskUserHistory(
        'Question',
        JSON.stringify({ version: 1, status: 'deferred', action: 'decide_for_me' })
      )
    ).toEqual({
      status: 'deferred',
      answer: null,
    });
    expect(
      normalizeXScienceAskUserHistory(
        'Question',
        JSON.stringify({ version: 1, status: 'discussed', action: 'discuss', message: 'Need more evidence' })
      )
    ).toEqual({ status: 'discussed', answer: null });
    expect(
      normalizeXScienceAskUserHistory('Question', JSON.stringify({ version: 1, status: 'cancelled', action: 'cancel' }))
    ).toEqual({ status: 'cancelled', answer: null });
  });

  it('fails closed for prose, unknown versions, and extra fields', () => {
    expect(normalizeXScienceAskUserHistory('Question', 'User cancelled this request')).toEqual({
      status: 'cancelled',
      answer: null,
    });
    expect(normalizeXScienceAskUserHistory('Question', 'The user wants to discuss this further')).toEqual({
      status: 'discussed',
      answer: null,
    });
    expect(normalizeXScienceAskUserHistory('Question', JSON.stringify({ action: 'decide_for_me' }))).toEqual({
      status: 'deferred',
      answer: null,
    });
    expect(
      normalizeXScienceAskUserHistory('Question', JSON.stringify({ version: 2, status: 'cancelled', action: 'cancel' }))
    ).toEqual({ status: 'unavailable', answer: null });
    expect(
      normalizeXScienceAskUserHistory(
        'Question',
        JSON.stringify({ version: 1, status: 'cancelled', action: 'cancel', detail: 'guess' })
      )
    ).toEqual({ status: 'unavailable', answer: null });
  });
});
