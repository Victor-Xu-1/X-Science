import './AskUserCard.css';
import { Button, Input } from '@arco-design/web-react';
import { CheckOne, CloseOne, Edit } from '@icon-park/react';
import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { XScienceAskUserResponse } from '@/renderer/services/xScienceRuntimeOperations';
import { renderMoleculeSvg } from '@/renderer/services/rdkitBrowser';
import {
  isXScienceAgentChoiceOption,
  type XScienceAskUserOption,
  type XScienceAskUserQuestion,
} from './runtimeOperationsModel';

type AskUserCardProps = {
  question: XScienceAskUserQuestion;
  questions?: XScienceAskUserQuestion[];
  disabled?: boolean;
  busy?: boolean;
  onResolve: (response: XScienceAskUserResponse) => void | Promise<void>;
  onBack?: () => void;
};

const AGENT_CHOICE = '__x_science_agent_choice__';
const AskUserCard: React.FC<AskUserCardProps> = ({
  question: initialQuestion,
  questions,
  disabled = false,
  busy = false,
  onResolve,
  onBack,
}) => {
  const { t } = useTranslation();
  const questionnaire = questions?.length ? questions : [initialQuestion];
  const questionnaireIdentity = JSON.stringify(
    questionnaire.map((item) => [item.question, item.multiSelect, item.options.map((option) => option.label)])
  );
  const previousQuestionnaireIdentity = useRef(questionnaireIdentity);
  const [questionIndex, setQuestionIndex] = useState(0);
  const [answers, setAnswers] = useState<Record<string, string>>({});
  const question = questionnaire[Math.min(questionIndex, questionnaire.length - 1)] ?? initialQuestion;
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [customAnswer, setCustomAnswer] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const submittingRef = useRef(false);
  const [submitFailed, setSubmitFailed] = useState(false);
  const visibleOptions = useMemo(
    () => question.options.filter((option) => !isXScienceAgentChoiceOption(option.label)),
    [question.options]
  );
  const optionLabels = useMemo(() => new Set(visibleOptions.map((option) => option.label)), [visibleOptions]);
  const customSelections = useMemo(
    () => Array.from(selected).filter((label) => label !== AGENT_CHOICE && !optionLabels.has(label)),
    [optionLabels, selected]
  );
  const inactive = disabled || busy || submitting;
  const selectedCount = selected.size;

  useEffect(() => {
    if (previousQuestionnaireIdentity.current === questionnaireIdentity) return;
    previousQuestionnaireIdentity.current = questionnaireIdentity;
    setQuestionIndex(0);
    setAnswers({});
    setSelected(new Set());
    setCustomAnswer('');
    setSubmitFailed(false);
  }, [questionnaireIdentity]);

  const resolve = async (response: XScienceAskUserResponse) => {
    if (inactive || submittingRef.current) return;
    submittingRef.current = true;
    setSubmitFailed(false);
    setSubmitting(true);
    try {
      await onResolve(response);
    } catch {
      setSubmitFailed(true);
    } finally {
      submittingRef.current = false;
      setSubmitting(false);
    }
  };

  const toggle = async (label: string) => {
    if (inactive) return;
    if (!question.multiSelect) {
      setSelected(new Set([label]));
      const nextAnswers = { ...answers, [question.question]: label };
      if (questionIndex + 1 < questionnaire.length) {
        setAnswers(nextAnswers);
        setQuestionIndex((index) => index + 1);
        setSelected(new Set());
        setCustomAnswer('');
      } else {
        await resolve({ action: 'answer', answers: nextAnswers });
      }
      return;
    }
    setSelected((current) => {
      const next = new Set(current);
      next.delete(AGENT_CHOICE);
      if (next.has(label)) next.delete(label);
      else next.add(label);
      return next;
    });
  };

  const submitSelection = async () => {
    if (inactive || selectedCount === 0) return;
    if (selected.has(AGENT_CHOICE)) {
      await resolve({ action: 'decide_for_me' });
      return;
    }
    const nextAnswers = { ...answers, [question.question]: Array.from(selected).join(', ') };
    if (questionIndex + 1 < questionnaire.length) {
      setAnswers(nextAnswers);
      setQuestionIndex((index) => index + 1);
      setSelected(new Set());
      setCustomAnswer('');
    } else {
      await resolve({ action: 'answer', answers: nextAnswers });
    }
  };

  const submitCustomAnswer = async () => {
    const answer = customAnswer.trim();
    if (inactive || !answer) return;
    if (question.multiSelect) {
      setSelected((current) => {
        const next = new Set(current);
        next.delete(AGENT_CHOICE);
        next.add(answer);
        return next;
      });
      setCustomAnswer('');
      return;
    }
    setSelected(new Set([answer]));
    const nextAnswers = { ...answers, [question.question]: answer };
    if (questionIndex + 1 < questionnaire.length) {
      setAnswers(nextAnswers);
      setQuestionIndex((index) => index + 1);
      setSelected(new Set());
      setCustomAnswer('');
    } else {
      await resolve({ action: 'answer', answers: nextAnswers });
    }
  };

  const selectAgentChoice = async () => {
    if (inactive) return;
    setSelected(new Set([AGENT_CHOICE]));
    if (!question.multiSelect) await resolve({ action: 'decide_for_me' });
  };

  return (
    <section
      data-testid='x-science-ask-user-card'
      aria-label={question.header ?? t('conversation.xscienceRuntime.askUser.responseRequired')}
      className='x-science-ask-user-card w-full min-w-0 box-border'
    >
      {question.header ? <div className='mb-4px text-12px font-[600] text-t-tertiary'>{question.header}</div> : null}
      {questionnaire.length > 1 ? (
        <div className='mb-4px text-12px text-t-tertiary'>
          {questionIndex + 1} / {questionnaire.length}
        </div>
      ) : null}
      <h3 className='m-0'>{question.question}</h3>
      {question.stageProgress ? (
        <div className='x-science-ask-user-card__stage mt-10px' data-testid='x-science-plan-stage-progress'>
          <p className='m-0 text-12px text-t-tertiary'>
            {t('conversation.xscienceRuntime.askUser.stageRecorded', { count: question.stageProgress.completedCount })}
          </p>
          <ul className='my-4px pl-18px'>
            {question.stageProgress.completedSteps.map((step) => (
              <li key={step.id}>{step.title}</li>
            ))}
          </ul>
          <p className='m-0 text-12px text-t-tertiary'>
            {t('conversation.xscienceRuntime.askUser.stageRemaining', { count: question.stageProgress.remainingCount })}
          </p>
          <ul className='my-4px pl-18px'>
            {question.stageProgress.remainingSteps.map((step) => (
              <li key={step.id}>{step.title}</li>
            ))}
          </ul>
        </div>
      ) : null}
      {submitFailed ? (
        <p role='alert'>{t('conversation.xscienceRuntime.runtimeOperations.answerSubmitFailed')}</p>
      ) : null}

      <div
        className='x-science-ask-user-card__options mt-10px flex flex-col gap-2px'
        role={question.multiSelect ? 'group' : 'radiogroup'}
      >
        {visibleOptions.map((option, index) => (
          <AskUserOptionRow
            key={option.label}
            option={option}
            ordinal={index + 1}
            selected={selected.has(option.label)}
            multiSelect={question.multiSelect}
            disabled={inactive}
            onSelect={() => void toggle(option.label)}
          />
        ))}
        <AskUserOptionRow
          option={{
            label: t('conversation.xscienceRuntime.askUser.decideForMe'),
            description: t('conversation.xscienceRuntime.askUser.decideForMeDescription'),
            pros: null,
            cons: null,
            smiles: null,
            recommended: false,
            readinessStatus: null,
          }}
          selected={selected.has(AGENT_CHOICE)}
          multiSelect={false}
          disabled={inactive}
          onSelect={() => void selectAgentChoice()}
        />
      </div>

      {customSelections.length > 0 ? (
        <div
          className='mt-8px flex flex-wrap items-center gap-6px px-4px'
          aria-label={t('conversation.xscienceRuntime.askUser.customSelections')}
        >
          <span className='text-12px text-t-tertiary'>{t('conversation.xscienceRuntime.askUser.customLabel')}</span>
          {customSelections.map((answer) => (
            <span
              key={answer}
              className='inline-flex min-w-0 items-center gap-4px rounded-full bg-fill-2 px-8px py-4px text-12px text-t-primary'
            >
              <span className='max-w-220px truncate'>{answer}</span>
              <button
                type='button'
                aria-label={t('conversation.xscienceRuntime.askUser.removeCustomAnswer', { answer })}
                className='size-18px shrink-0 flex items-center justify-center rounded-full border-0 bg-transparent text-t-tertiary hover:bg-fill-3 hover:text-t-primary'
                disabled={inactive}
                onClick={() =>
                  setSelected((current) => {
                    const next = new Set(current);
                    next.delete(answer);
                    return next;
                  })
                }
              >
                <CloseOne theme='outline' size={11} />
              </button>
            </span>
          ))}
        </div>
      ) : null}

      <div
        data-composite-focus-owner
        className='x-science-ask-user-card__composer mt-10px flex min-w-0 items-center gap-8px rounded-10px bg-fill-1 px-8px py-4px'
      >
        <Edit theme='outline' size={15} className='shrink-0 text-t-tertiary' />
        <Input
          className='x-science-ask-user-card__input min-w-0 flex-1'
          value={customAnswer}
          disabled={inactive}
          aria-label={t('conversation.xscienceRuntime.askUser.customAnswer')}
          placeholder={t('conversation.xscienceRuntime.askUser.customAnswerPlaceholder')}
          onChange={setCustomAnswer}
          onPressEnter={() => void submitCustomAnswer()}
        />
        {customAnswer.trim() ? (
          <Button
            type='primary'
            className='x-science-ask-user-card__submit'
            aria-label={t('conversation.xscienceRuntime.askUser.sendAnswer')}
            loading={busy || submitting}
            disabled={inactive}
            icon={<CheckOne theme='outline' size={14} />}
            onClick={() => void submitCustomAnswer()}
          >
            {t('conversation.xscienceRuntime.askUser.send')}
          </Button>
        ) : null}
      </div>

      <div className='x-science-ask-user-card__actions mt-8px flex flex-wrap items-center gap-4px'>
        {onBack ? (
          <Button type='text' className='x-science-ask-user-card__action--quiet' disabled={inactive} onClick={onBack}>
            {t('conversation.xscienceRuntime.askUser.back')}
          </Button>
        ) : null}
        <Button
          type='text'
          className='x-science-ask-user-card__action--quiet'
          disabled={inactive}
          onClick={() =>
            void resolve({
              action: 'discuss',
              message: t('conversation.xscienceRuntime.askUser.discussMessage'),
            })
          }
        >
          {t('conversation.xscienceRuntime.askUser.discuss')}
        </Button>
        <Button
          type='text'
          className='x-science-ask-user-card__action--quiet'
          disabled={inactive}
          onClick={() => void resolve({ action: 'cancel' })}
        >
          {t('conversation.xscienceRuntime.askUser.skip')}
        </Button>
        <span className='min-w-0 flex-1 text-right text-12px text-t-tertiary'>
          {selectedCount > 0 ? t('conversation.xscienceRuntime.askUser.selectedCount', { count: selectedCount }) : ''}
        </span>
        {question.multiSelect ? (
          <Button
            type='primary'
            className='x-science-ask-user-card__submit'
            loading={busy || submitting}
            disabled={inactive || selectedCount === 0}
            icon={<CheckOne theme='outline' size={14} />}
            onClick={() => void submitSelection()}
          >
            {t('conversation.xscienceRuntime.askUser.submit')}
          </Button>
        ) : null}
      </div>
    </section>
  );
};

const AskUserOptionRow: React.FC<{
  option: XScienceAskUserOption;
  ordinal?: number;
  selected: boolean;
  multiSelect: boolean;
  disabled: boolean;
  onSelect: () => void;
}> = ({ option, ordinal, selected, multiSelect, disabled, onSelect }) => {
  const { t } = useTranslation();
  const readinessLabel =
    option.readinessStatus === 'verified_ready'
      ? t('conversation.xscienceRuntime.askUser.readinessReady')
      : option.readinessStatus === 'configured'
        ? t('conversation.xscienceRuntime.askUser.readinessConfigured')
        : option.readinessStatus === 'unverified'
          ? t('conversation.xscienceRuntime.askUser.readinessUnverified')
          : null;
  return (
    <button
      type='button'
      role={multiSelect ? 'checkbox' : 'radio'}
      aria-checked={selected}
      disabled={disabled}
      className={`x-science-ask-user-card__option w-full min-w-0 border-0 bg-transparent px-4px py-10px text-left transition-colors disabled:opacity-60 ${
        selected ? 'bg-fill-2' : 'hover:bg-fill-1'
      }`}
      onClick={onSelect}
    >
      <span className='flex min-w-0 items-center gap-10px'>
        <span
          aria-hidden='true'
          className={`size-26px shrink-0 flex items-center justify-center rounded-6px text-12px font-[600] ${
            selected ? 'bg-primary text-white' : 'bg-fill-2 text-t-secondary'
          }`}
        >
          {ordinal ?? <CheckOne theme='outline' size={13} />}
        </span>
        {option.smiles ? <MoleculeThumbnail smiles={option.smiles} /> : null}
        <span className='min-w-0 flex-1'>
          <span className='flex min-w-0 items-center gap-6px'>
            <span className='min-w-0 text-13px leading-20px font-[500] text-t-primary'>{option.label}</span>
            {option.recommended ? (
              <span className='shrink-0 rounded-5px bg-fill-2 px-5px py-1px text-10px leading-14px font-[500] text-t-secondary'>
                {t('conversation.xscienceRuntime.askUser.recommended')}
              </span>
            ) : null}
            {readinessLabel ? (
              <span className='shrink-0 rounded-5px bg-fill-1 px-5px py-1px text-10px leading-14px font-[500] text-t-tertiary'>
                {readinessLabel}
              </span>
            ) : null}
          </span>
          {option.description ? (
            <span className='mt-2px block whitespace-pre-line text-12px leading-18px text-t-secondary'>
              {option.description}
            </span>
          ) : null}
          {option.pros ? (
            <span className='mt-3px block text-12px text-success-6'>
              {t('conversation.xscienceRuntime.askUser.pros', { value: option.pros })}
            </span>
          ) : null}
          {option.cons ? (
            <span className='mt-2px block text-12px text-warning-6'>
              {t('conversation.xscienceRuntime.askUser.cons', { value: option.cons })}
            </span>
          ) : null}
        </span>
      </span>
    </button>
  );
};

const MoleculeThumbnail: React.FC<{ smiles: string }> = ({ smiles }) => {
  const { t } = useTranslation();
  const [svg, setSvg] = useState<string | null>(null);
  const [failed, setFailed] = useState(false);
  const source = useMemo(() => smiles.trim(), [smiles]);

  useEffect(() => {
    let active = true;
    setSvg(null);
    setFailed(false);
    void renderMoleculeSvg(source, 88, 64)
      .then((rendered) => {
        if (!active) return;
        if (!rendered) {
          setFailed(true);
          return;
        }
        setSvg(rendered);
      })
      .catch(() => {
        if (active) setFailed(true);
      });
    return () => {
      active = false;
    };
  }, [source]);

  if (svg) {
    return (
      <img
        src={`data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`}
        alt={t('conversation.xscienceRuntime.askUser.moleculeStructure', { smiles: source })}
        className='h-64px w-88px shrink-0 bg-white object-contain p-4px'
      />
    );
  }
  return failed ? (
    <span
      aria-label={t('conversation.xscienceRuntime.askUser.moleculeFailed', { smiles: source })}
      title={source}
      className='h-64px w-88px shrink-0 flex flex-col items-center justify-center rounded-4px bg-fill-2 px-5px text-center'
    >
      <span className='text-9px font-[600] text-t-tertiary'>SMILES</span>
      <code className='mt-2px block w-full truncate text-9px text-t-secondary'>{source}</code>
    </span>
  ) : (
    <span
      aria-label={t('conversation.xscienceRuntime.askUser.moleculeLoading', { smiles: source })}
      className='h-64px w-88px shrink-0 animate-pulse bg-fill-2'
    />
  );
};

export default AskUserCard;
