import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { CreateJob, DeleteJob, GetJobs, GetJobRunLog, GetJobRuns, RunJobNow, UpdateJob } from '../wailsjs/go/main/ProjectManager';
import { main as wailsMain } from '../wailsjs/go/models';
import { Icon } from './icons';
import type { Job, JobDefinitionInput, JobKind, JobRun, JobRunStatus, JobSchedule } from './types';
import './JobsView.css';

const jobsRefreshInterval = 15_000;
const maxVisibleLogLines = 250;

function formatDateTime(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return 'Time unavailable';
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(date);
}

function formatClock(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return 'Unknown';
  return new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit' }).format(date);
}

function compareNextRun(left: Job, right: Job): number {
  const leftTime = new Date(left.nextRunAt).getTime();
  const rightTime = new Date(right.nextRunAt).getTime();
  return (Number.isNaN(leftTime) ? Number.POSITIVE_INFINITY : leftTime)
    - (Number.isNaN(rightTime) ? Number.POSITIVE_INFINITY : rightTime);
}

function formatInterval(schedule: JobSchedule): string {
  const intervalLabel = schedule.intervalMinutes === 1 ? 'minute' : 'minutes';
  return `Every ${schedule.intervalMinutes} ${intervalLabel} · offset ${schedule.offsetMinutes} min`;
}

type JobRunBinding = Omit<JobRun, 'status'> & { status: string };
type JobBinding = Omit<Job, 'kind' | 'lastRun'> & { kind: string; lastRun?: JobRunBinding };
type ManualRunMessage = { kind: 'status' | 'error'; text: string };
type JobEditorMode = 'create' | 'edit' | null;
type JobDraft = Omit<JobDefinitionInput, 'schedule'> & {
  intervalMinutes: string;
  offsetMinutes: string;
};

const jobKindOptions: ReadonlyArray<{ value: JobKind; label: string; help: string }> = [
  { value: 'project-health', label: 'Project health check', help: 'Checks the configured backend for a loaded project.' },
  { value: 'minikube-port-forwards', label: 'Minikube port forwards', help: 'Checks and safely recovers supported local port forwards.' },
  { value: 'openchamber-free-usage', label: 'OpenChamber free usage', help: 'Checks free usage with a project-scoped scheduled session.' },
];

function emptyJobDraft(): JobDraft {
  return {
    name: '',
    description: '',
    kind: 'project-health',
    projectName: '',
    enabled: true,
    intervalMinutes: '60',
    offsetMinutes: '0',
  };
}

function draftFromJob(job: Job): JobDraft {
  return {
    name: job.name,
    description: job.description,
    kind: job.kind,
    projectName: job.projectName ?? '',
    enabled: job.enabled,
    intervalMinutes: String(job.schedule.intervalMinutes),
    offsetMinutes: String(job.schedule.offsetMinutes),
  };
}

function jobKindLabel(kind: JobKind): string {
  return jobKindOptions.find((option) => option.value === kind)?.label ?? kind;
}

function definitionFromDraft(draft: JobDraft): JobDefinitionInput {
  const name = draft.name.trim();
  const description = draft.description.trim();
  const projectName = draft.kind === 'project-health' ? draft.projectName.trim() : '';
  const intervalMinutes = Number(draft.intervalMinutes);
  const offsetMinutes = Number(draft.offsetMinutes);
  if (!name) throw new Error('A job name is required.');
  if ([...name].length > 80) throw new Error('Keep the job name to 80 characters or fewer.');
  if ([...description].length > 1000) throw new Error('Keep the job description to 1000 characters or fewer.');
  if (draft.kind === 'project-health' && !projectName) throw new Error('A project name is required for a health check job.');
  if ([...projectName].length > 120) throw new Error('Keep the project name to 120 characters or fewer.');
  if (!Number.isInteger(intervalMinutes) || intervalMinutes < 1 || intervalMinutes > 1440) {
    throw new Error('Enter an interval from 1 to 1440 whole minutes.');
  }
  if (!Number.isInteger(offsetMinutes) || offsetMinutes < 0 || offsetMinutes >= intervalMinutes) {
    throw new Error('Offset must be a whole number from zero to one less than the interval.');
  }
  return {
    name,
    description,
    kind: draft.kind,
    projectName,
    enabled: draft.enabled,
    schedule: { intervalMinutes, offsetMinutes },
  };
}

const supportedRunStatuses = new Set<string>(['running', 'healthy', 'unhealthy', 'failed']);
const supportedJobKinds = new Set<string>(jobKindOptions.map((option) => option.value));

function normalizeRun(run: JobRunBinding): JobRun {
  if (!supportedRunStatuses.has(run.status)) throw new Error(`Unsupported job run status: ${run.status}`);
  return { ...run, status: run.status as JobRunStatus };
}

function normalizeJob(job: JobBinding): Job {
  if (!supportedJobKinds.has(job.kind)) throw new Error(`Unsupported job type: ${job.kind}`);
  return { ...job, kind: job.kind as JobKind, lastRun: job.lastRun ? normalizeRun(job.lastRun) : undefined };
}

const runStatusLabel: Record<JobRun['status'], string> = {
  running: 'Running',
  healthy: 'Healthy',
  unhealthy: 'Unhealthy',
  failed: 'Failed',
};

type JobEditorFormProps = {
  mode: Exclude<JobEditorMode, null>;
  draft: JobDraft;
  saving: boolean;
  error: string;
  onDraftChange: (patch: Partial<JobDraft>) => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
  onCancel: () => void;
};

function JobEditorForm({ mode, draft, saving, error, onDraftChange, onSubmit, onCancel }: JobEditorFormProps) {
  const idPrefix = `job-${mode}-form`;
  const kindHelpID = `${idPrefix}-kind-help`;
  const scheduleHintID = `${idPrefix}-schedule-hint`;
  const isCreate = mode === 'create';

  return (
    <form id={idPrefix} className="jobs-definition-form" onSubmit={onSubmit} aria-labelledby={`${idPrefix}-title`} aria-busy={saving}>
      <div className="jobs-editor-heading">
        <div>
          <p className="eyebrow">{isCreate ? 'NEW DEFINITION' : 'EDIT DEFINITION'}</p>
          <h3 id={`${idPrefix}-title`}>{isCreate ? 'Create a scheduled job' : 'Edit job details'}</h3>
        </div>
      </div>

      <div className="jobs-definition-fields">
        <label className="jobs-editor-field" htmlFor={`${idPrefix}-name`}>
          <span>Job name</span>
          <input
            id={`${idPrefix}-name`}
            type="text"
            maxLength={80}
            required
            value={draft.name}
            onChange={(event) => onDraftChange({ name: event.target.value })}
            disabled={saving}
          />
        </label>
        <label className="jobs-editor-field jobs-editor-description" htmlFor={`${idPrefix}-description`}>
          <span>Description <small>(optional)</small></span>
          <textarea
            id={`${idPrefix}-description`}
            rows={2}
            maxLength={1000}
            value={draft.description}
            onChange={(event) => onDraftChange({ description: event.target.value })}
            disabled={saving}
          />
        </label>
      </div>

      {isCreate ? (
        <label className="jobs-editor-field" htmlFor={`${idPrefix}-kind`}>
          <span>Job type</span>
          <select
            id={`${idPrefix}-kind`}
            value={draft.kind}
            onChange={(event) => {
              const kind = event.target.value as JobKind;
              onDraftChange({ kind, projectName: kind === 'project-health' ? draft.projectName : '' });
            }}
            aria-describedby={kindHelpID}
            disabled={saving}
          >
            {jobKindOptions.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
          </select>
          <small id={kindHelpID}>{jobKindOptions.find((option) => option.value === draft.kind)?.help}</small>
        </label>
      ) : (
        <div className="jobs-editor-kind" aria-describedby={kindHelpID}>
          <span>Job type</span>
          <strong>{jobKindLabel(draft.kind)}</strong>
          <small id={kindHelpID}>Job type stays fixed after creation.</small>
        </div>
      )}

      {draft.kind === 'project-health' && (
        <label className="jobs-editor-field" htmlFor={`${idPrefix}-project`}>
          <span>Project name</span>
          <input
            id={`${idPrefix}-project`}
            type="text"
            maxLength={120}
            required
            value={draft.projectName}
            onChange={(event) => onDraftChange({ projectName: event.target.value })}
            aria-describedby={`${idPrefix}-project-help`}
            disabled={saving}
          />
          <small id={`${idPrefix}-project-help`}>The check resolves a loaded project with this name when it runs.</small>
        </label>
      )}

      <div className="jobs-schedule-fields">
        <label htmlFor={`${idPrefix}-interval`}>
          Interval <span>(minutes)</span>
          <input
            id={`${idPrefix}-interval`}
            type="number"
            min="1"
            max="1440"
            step="1"
            required
            value={draft.intervalMinutes}
            onChange={(event) => onDraftChange({ intervalMinutes: event.target.value })}
            aria-describedby={scheduleHintID}
            disabled={saving}
          />
        </label>
        <label htmlFor={`${idPrefix}-offset`}>
          Offset <span>(minutes)</span>
          <input
            id={`${idPrefix}-offset`}
            type="number"
            min="0"
            max={Math.max(0, (Number(draft.intervalMinutes) || 1) - 1)}
            step="1"
            required
            value={draft.offsetMinutes}
            onChange={(event) => onDraftChange({ offsetMinutes: event.target.value })}
            aria-describedby={scheduleHintID}
            disabled={saving}
          />
        </label>
      </div>
      <p id={scheduleHintID} className="jobs-form-hint">Use 1–1440 whole minutes; offset must be less than the interval.</p>

      <label className="jobs-enabled-toggle" htmlFor={`${idPrefix}-enabled`}>
        <input
          id={`${idPrefix}-enabled`}
          type="checkbox"
          checked={draft.enabled}
          onChange={(event) => onDraftChange({ enabled: event.target.checked })}
          disabled={saving}
        />
        <span>
          <strong>Enabled</strong>
          <small>Paused jobs stay in the list and can still be run manually.</small>
        </span>
      </label>

      {error && <p className="jobs-inline-error" role="alert" aria-live="assertive">{error}</p>}
      <div className="jobs-form-actions">
        <button type="button" className="secondary-button" onClick={onCancel} disabled={saving}>Cancel</button>
        <button type="submit" className="primary-button jobs-save-button" disabled={saving}>
          {saving ? (isCreate ? 'Creating…' : 'Saving…') : (isCreate ? 'Create job' : 'Save job')}
        </button>
      </div>
    </form>
  );
}

export function JobsView() {
  const [jobs, setJobs] = useState<Job[]>([]);
  const [jobsLoading, setJobsLoading] = useState(true);
  const [jobsRefreshing, setJobsRefreshing] = useState(false);
  const [jobsError, setJobsError] = useState('');
  const [selectedJobId, setSelectedJobId] = useState('');
  const selectedJobIdRef = useRef('');
  const jobsRequestRef = useRef(0);
  const [jobsRevision, setJobsRevision] = useState(0);

  const [runs, setRuns] = useState<JobRun[]>([]);
  const [runsLoading, setRunsLoading] = useState(false);
  const [runsError, setRunsError] = useState('');
  const [selectedRunId, setSelectedRunId] = useState('');
  const selectedRunIdRef = useRef('');
  const runsRequestRef = useRef(0);
  const [runsRevision, setRunsRevision] = useState(0);

  const [logLines, setLogLines] = useState<string[]>([]);
  const [logLoading, setLogLoading] = useState(false);
  const [logError, setLogError] = useState('');
  const [logRetryRevision, setLogRetryRevision] = useState(0);
  const logRequestRef = useRef(0);
  const loadedLogKeyRef = useRef('');

  const [jobEditorMode, setJobEditorMode] = useState<JobEditorMode>(null);
  const [jobDraft, setJobDraft] = useState<JobDraft>(emptyJobDraft);
  const [jobMutationBusy, setJobMutationBusy] = useState(false);
  const [jobEditorError, setJobEditorError] = useState('');
  const [jobNotice, setJobNotice] = useState('');
  const [pendingDeleteJobId, setPendingDeleteJobId] = useState('');
  const [jobActionError, setJobActionError] = useState('');
  const [pendingManualRunIds, setPendingManualRunIds] = useState<Set<string>>(() => new Set());
  const pendingManualRunIdsRef = useRef(new Set<string>());
  const [manualRunMessages, setManualRunMessages] = useState<Record<string, ManualRunMessage>>({});

  const sortedJobs = useMemo(() => [...jobs].sort(compareNextRun), [jobs]);
  const selectedJob = sortedJobs.find((job) => job.id === selectedJobId) ?? null;
  const selectedRun = runs.find((run) => run.id === selectedRunId) ?? null;
  const visibleLogLines = useMemo(() => logLines.slice(-maxVisibleLogLines), [logLines]);

  const refreshJobs = useCallback(async (showLoading: boolean) => {
    const requestID = ++jobsRequestRef.current;
    if (showLoading) setJobsLoading(true);
    else setJobsRefreshing(true);
    try {
      const nextJobs = (await GetJobs()).map(normalizeJob);
      if (jobsRequestRef.current !== requestID) return;
      setJobs(nextJobs);
      setJobsError('');

      const currentJobID = selectedJobIdRef.current;
      const nextJobID = currentJobID && nextJobs.some((job) => job.id === currentJobID)
        ? currentJobID
        : [...nextJobs].sort(compareNextRun)[0]?.id ?? '';
      if (nextJobID !== currentJobID) {
        selectedJobIdRef.current = nextJobID;
        setSelectedJobId(nextJobID);
        selectedRunIdRef.current = '';
        setSelectedRunId('');
        setRuns([]);
        setRunsError('');
        setRunsLoading(Boolean(nextJobID));
        setLogLines([]);
        setLogError('');
        setLogLoading(false);
        loadedLogKeyRef.current = '';
        setJobEditorMode(null);
        setJobEditorError('');
        setJobNotice('');
        setPendingDeleteJobId('');
        setJobActionError('');
      }
      setJobsRevision((revision) => revision + 1);
    } catch (caught) {
      if (jobsRequestRef.current === requestID) {
        setJobsError(caught instanceof Error ? caught.message : String(caught));
      }
    } finally {
      if (jobsRequestRef.current === requestID) {
        setJobsLoading(false);
        setJobsRefreshing(false);
      }
    }
  }, []);

  useEffect(() => {
    void refreshJobs(true);
    const timer = window.setInterval(() => { void refreshJobs(false); }, jobsRefreshInterval);
    return () => {
      window.clearInterval(timer);
      jobsRequestRef.current += 1;
    };
  }, [refreshJobs]);

  useEffect(() => {
    if (!selectedJobId) {
      setRuns([]);
      setRunsLoading(false);
      return;
    }

    let cancelled = false;
    const requestID = ++runsRequestRef.current;
    if (!runs.length) setRunsLoading(true);
    void GetJobRuns(selectedJobId).then((nextRuns) => {
      if (cancelled || runsRequestRef.current !== requestID) return;
      const newestFirst = nextRuns.map(normalizeRun).sort((left, right) => {
        const leftTime = new Date(left.startedAt).getTime();
        const rightTime = new Date(right.startedAt).getTime();
        return (Number.isNaN(rightTime) ? 0 : rightTime) - (Number.isNaN(leftTime) ? 0 : leftTime);
      });
      setRuns(newestFirst);
      setRunsError('');

      const currentRunID = selectedRunIdRef.current;
      const nextRunID = currentRunID && newestFirst.some((run) => run.id === currentRunID)
        ? currentRunID
        : newestFirst[0]?.id ?? '';
      if (nextRunID !== currentRunID) {
        selectedRunIdRef.current = nextRunID;
        setSelectedRunId(nextRunID);
        setLogLines([]);
        setLogError('');
        setLogLoading(Boolean(nextRunID));
        loadedLogKeyRef.current = '';
      }
      setRunsRevision((revision) => revision + 1);
    }).catch((caught: unknown) => {
      if (!cancelled && runsRequestRef.current === requestID) {
        setRunsError(caught instanceof Error ? caught.message : String(caught));
      }
    }).finally(() => {
      if (!cancelled && runsRequestRef.current === requestID) setRunsLoading(false);
    });

    return () => {
      cancelled = true;
      runsRequestRef.current += 1;
    };
  }, [selectedJobId, jobsRevision]);

  useEffect(() => {
    if (!selectedJobId || !selectedRunId) {
      setLogLines([]);
      setLogLoading(false);
      return;
    }

    let cancelled = false;
    const requestID = ++logRequestRef.current;
    const logKey = `${selectedJobId}\u0000${selectedRunId}`;
    if (loadedLogKeyRef.current !== logKey) setLogLoading(true);
    void GetJobRunLog(selectedJobId, selectedRunId).then((nextLines) => {
      if (cancelled || logRequestRef.current !== requestID) return;
      setLogLines(nextLines);
      setLogError('');
      loadedLogKeyRef.current = logKey;
    }).catch((caught: unknown) => {
      if (!cancelled && logRequestRef.current === requestID) {
        setLogError(caught instanceof Error ? caught.message : String(caught));
      }
    }).finally(() => {
      if (!cancelled && logRequestRef.current === requestID) setLogLoading(false);
    });

    return () => {
      cancelled = true;
      logRequestRef.current += 1;
    };
  }, [selectedJobId, selectedRunId, runsRevision, logRetryRevision]);

  const selectJob = (jobID: string) => {
    if (jobID === selectedJobId || jobMutationBusy) return;
    selectedJobIdRef.current = jobID;
    setSelectedJobId(jobID);
    selectedRunIdRef.current = '';
    setSelectedRunId('');
    setRuns([]);
    setRunsError('');
    setRunsLoading(true);
    setLogLines([]);
    setLogError('');
    setLogLoading(false);
    loadedLogKeyRef.current = '';
    setJobEditorMode(null);
    setJobEditorError('');
    setJobNotice('');
    setPendingDeleteJobId('');
    setJobActionError('');
  };

  const selectRun = (runID: string) => {
    if (runID === selectedRunId) return;
    selectedRunIdRef.current = runID;
    setSelectedRunId(runID);
    setLogLines([]);
    setLogError('');
    setLogLoading(Boolean(runID));
    loadedLogKeyRef.current = '';
  };

  const openCreateJob = () => {
    setJobDraft(emptyJobDraft());
    setJobEditorMode('create');
    setJobEditorError('');
    setJobNotice('');
    setJobActionError('');
    setPendingDeleteJobId('');
  };

  const openEditJob = () => {
    if (!selectedJob) return;
    setJobDraft(draftFromJob(selectedJob));
    setJobEditorMode('edit');
    setJobEditorError('');
    setJobNotice('');
    setJobActionError('');
    setPendingDeleteJobId('');
  };

  const cancelJobEditor = () => {
    setJobEditorMode(null);
    setJobEditorError('');
  };

  const saveJob = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!jobEditorMode || jobMutationBusy || (jobEditorMode === 'edit' && !selectedJob)) return;

    let input: JobDefinitionInput;
    try {
      input = definitionFromDraft(jobDraft);
    } catch (caught) {
      setJobEditorError(caught instanceof Error ? caught.message : String(caught));
      return;
    }

    const editorMode = jobEditorMode;
    const jobID = selectedJob?.id;
    setJobMutationBusy(true);
    setJobEditorError('');
    setJobNotice('');
    setJobActionError('');
    try {
      if (editorMode === 'create') {
        const created = normalizeJob(await CreateJob(new wailsMain.JobDefinitionInput(input)));
        jobsRequestRef.current += 1;
        setJobsLoading(false);
        setJobsRefreshing(false);
        setJobs((current) => [...current.filter((job) => job.id !== created.id), created].sort(compareNextRun));
        setJobsError('');
        selectedJobIdRef.current = created.id;
        setSelectedJobId(created.id);
        selectedRunIdRef.current = '';
        setSelectedRunId('');
        setRuns([]);
        setRunsError('');
        setRunsLoading(true);
        setLogLines([]);
        setLogError('');
        setLogLoading(false);
        loadedLogKeyRef.current = '';
        setJobNotice(`${created.name} was created.`);
      } else if (jobID) {
        const updated = normalizeJob(await UpdateJob(jobID, new wailsMain.JobDefinitionInput(input)));
        jobsRequestRef.current += 1;
        setJobsLoading(false);
        setJobsRefreshing(false);
        setJobs((current) => current.map((job) => job.id === updated.id ? updated : job).sort(compareNextRun));
        setJobsError('');
        setJobNotice(`${updated.name} was saved.`);
      }
      setJobEditorMode(null);
    } catch (caught) {
      setJobEditorError(caught instanceof Error ? caught.message : String(caught));
    } finally {
      setJobMutationBusy(false);
    }
  };

  const toggleJobEnabled = async (job: Job) => {
    if (jobMutationBusy) return;
    setJobMutationBusy(true);
    setJobActionError('');
    setJobNotice('');
    try {
      const updated = normalizeJob(await UpdateJob(job.id, new wailsMain.JobDefinitionInput({
        name: job.name,
        description: job.description,
        kind: job.kind,
        projectName: job.projectName ?? '',
        enabled: !job.enabled,
        schedule: job.schedule,
      })));
      jobsRequestRef.current += 1;
      setJobsLoading(false);
      setJobsRefreshing(false);
      setJobs((current) => current.map((item) => item.id === updated.id ? updated : item).sort(compareNextRun));
      setJobsError('');
      setJobNotice(`${updated.name} ${updated.enabled ? 'resumed' : 'paused'}.`);
    } catch (caught) {
      setJobActionError(caught instanceof Error ? caught.message : String(caught));
    } finally {
      setJobMutationBusy(false);
    }
  };

  const deleteJob = async (job: Job) => {
    if (jobMutationBusy) return;
    setJobMutationBusy(true);
    setJobActionError('');
    setJobNotice('');
    try {
      await DeleteJob(job.id);
      const remainingJobs = jobs.filter((item) => item.id !== job.id).sort(compareNextRun);
      const nextSelectedJob = remainingJobs[0] ?? null;
      jobsRequestRef.current += 1;
      setJobsLoading(false);
      setJobsRefreshing(false);
      setJobs(remainingJobs);
      setJobsError('');
      selectedJobIdRef.current = nextSelectedJob?.id ?? '';
      setSelectedJobId(nextSelectedJob?.id ?? '');
      selectedRunIdRef.current = '';
      setSelectedRunId('');
      setRuns([]);
      setRunsError('');
      setRunsLoading(Boolean(nextSelectedJob));
      setLogLines([]);
      setLogError('');
      setLogLoading(false);
      loadedLogKeyRef.current = '';
      setJobEditorMode(null);
      setPendingDeleteJobId('');
      setJobNotice(`${job.name} was deleted.`);
    } catch (caught) {
      setJobActionError(caught instanceof Error ? caught.message : String(caught));
    } finally {
      setJobMutationBusy(false);
    }
  };

  const runJobNow = async (job: Job) => {
    if (pendingManualRunIdsRef.current.has(job.id)) return;
    pendingManualRunIdsRef.current.add(job.id);
    setPendingManualRunIds(new Set(pendingManualRunIdsRef.current));
    setManualRunMessages((current) => ({
      ...current,
      [job.id]: { kind: 'status', text: `Manual run request for ${job.name} is in progress.` },
    }));

    try {
      const run = normalizeRun(await RunJobNow(job.id));
      selectedRunIdRef.current = run.id;
      setSelectedRunId(run.id);
      setLogLines([]);
      setLogError('');
      setLogLoading(true);
      loadedLogKeyRef.current = '';
      setManualRunMessages((current) => ({
        ...current,
        [job.id]: { kind: 'status', text: `${job.name} finished: ${runStatusLabel[run.status]}. The run was added to history.` },
      }));
      await refreshJobs(false);
    } catch (caught) {
      setManualRunMessages((current) => ({
        ...current,
        [job.id]: {
          kind: 'error',
          text: `Manual run could not be started: ${caught instanceof Error ? caught.message : String(caught)}`,
        },
      }));
    } finally {
      pendingManualRunIdsRef.current.delete(job.id);
      setPendingManualRunIds(new Set(pendingManualRunIdsRef.current));
    }
  };

  return (
    <section className="jobs-screen" aria-labelledby="jobs-title">
      <header className="jobs-page-heading">
        <div>
          <p className="eyebrow">SCHEDULE</p>
          <h1 id="jobs-title">Scheduled jobs</h1>
          <p>Manage job definitions, see what runs next, and inspect recent results.</p>
        </div>
        <div className="jobs-heading-actions">
          <button
            type="button"
            className={jobEditorMode === 'create' ? 'secondary-button jobs-create-button' : 'primary-button jobs-create-button'}
            onClick={() => jobEditorMode === 'create' ? cancelJobEditor() : openCreateJob()}
            aria-expanded={jobEditorMode === 'create'}
            aria-controls={jobEditorMode === 'create' ? 'job-create-form' : undefined}
            disabled={jobMutationBusy}
          >
            {jobEditorMode === 'create' ? 'Close new job form' : 'New job'}
          </button>
          <button type="button" className="secondary-button jobs-refresh-button" onClick={() => void refreshJobs(true)} disabled={jobsLoading || jobsRefreshing || jobMutationBusy}>
            <Icon name="refresh" />{jobsLoading ? 'Loading jobs…' : jobsRefreshing ? 'Refreshing…' : 'Refresh jobs'}
          </button>
        </div>
      </header>

      {jobNotice && <p className="jobs-page-notice" role="status" aria-live="polite">{jobNotice}</p>}
      {jobEditorMode === 'create' && (
        <JobEditorForm
          mode="create"
          draft={jobDraft}
          saving={jobMutationBusy}
          error={jobEditorError}
          onDraftChange={(patch) => setJobDraft((current) => ({ ...current, ...patch }))}
          onSubmit={saveJob}
          onCancel={cancelJobEditor}
        />
      )}

      {jobsError && jobs.length > 0 && (
        <div className="jobs-error-banner" role="alert">
          <span>Unable to refresh jobs: {jobsError}</span>
          <button type="button" className="secondary-button" onClick={() => void refreshJobs(true)}>Retry</button>
        </div>
      )}

      {jobsLoading && jobs.length === 0 ? (
        <div className="jobs-state" role="status" aria-live="polite"><Icon name="refresh" /><span>Loading scheduled jobs…</span></div>
      ) : jobsError && jobs.length === 0 ? (
        <div className="jobs-state jobs-state-error" role="alert">
          <Icon name="warning" />
          <h2>Jobs could not be loaded</h2>
          <p>{jobsError}</p>
          <button type="button" className="secondary-button" onClick={() => void refreshJobs(true)}>Retry</button>
        </div>
      ) : jobs.length === 0 ? (
        <div className="jobs-state jobs-state-empty">
          <h2>No scheduled jobs</h2>
          <p>No jobs are registered yet. Use New job to add a supported check.</p>
        </div>
      ) : (
        <div className="jobs-layout">
          <section className="jobs-list-panel" aria-labelledby="jobs-next-title">
            <div className="jobs-panel-heading">
              <div>
                <p className="eyebrow">UPCOMING</p>
                <h2 id="jobs-next-title">Next scheduled</h2>
              </div>
              {jobsRefreshing && <span className="jobs-refreshing" role="status">Updating</span>}
            </div>
            <nav className="jobs-timeline" aria-label="Scheduled jobs ordered by next run">
              {sortedJobs.map((job) => {
                const selected = job.id === selectedJobId;
                return (
                  <button
                    key={job.id}
                    type="button"
                    className={`jobs-timeline-item${selected ? ' is-selected' : ''}`}
                    onClick={() => selectJob(job.id)}
                    aria-pressed={selected}
                    disabled={jobMutationBusy}
                  >
                    <span className="jobs-time-mark" aria-hidden="true" />
                    <time className="jobs-next-time" dateTime={job.nextRunAt} title={formatDateTime(job.nextRunAt)}>{formatClock(job.nextRunAt)}</time>
                    <span className="jobs-timeline-copy">
                      <strong>{job.name}</strong>
                      <small>{job.enabled ? (job.description || formatInterval(job.schedule)) : `Paused · ${job.description || formatInterval(job.schedule)}`}</small>
                    </span>
                  </button>
                );
              })}
            </nav>
          </section>

          <section className="jobs-detail-panel" aria-labelledby="job-detail-title">
            {selectedJob ? (
              <>
                <header className="jobs-detail-heading">
                  <div className="jobs-detail-title-copy">
                    <p className="eyebrow">JOB</p>
                    <h2 id="job-detail-title" title={selectedJob.name}>{selectedJob.name}</h2>
                    <p className="jobs-kind-summary">{jobKindLabel(selectedJob.kind)} <span>{selectedJob.enabled ? 'Enabled' : 'Paused'}</span></p>
                    {selectedJob.description && <p>{selectedJob.description}</p>}
                  </div>
                  <button
                    type="button"
                    className="jobs-run-now-button"
                    onClick={() => void runJobNow(selectedJob)}
                    disabled={pendingManualRunIds.has(selectedJob.id) || jobMutationBusy}
                    aria-busy={pendingManualRunIds.has(selectedJob.id)}
                    aria-label={`Run ${selectedJob.name} now`}
                  >
                    <Icon name="play" aria-hidden="true" />
                    {pendingManualRunIds.has(selectedJob.id) ? 'Running…' : 'Run now'}
                  </button>
                </header>
                {manualRunMessages[selectedJob.id] && (
                  <p
                    className={manualRunMessages[selectedJob.id].kind === 'error' ? 'jobs-manual-run-feedback jobs-inline-error' : 'jobs-manual-run-feedback jobs-inline-success'}
                    role={manualRunMessages[selectedJob.id].kind === 'error' ? 'alert' : 'status'}
                    aria-live={manualRunMessages[selectedJob.id].kind === 'error' ? 'assertive' : 'polite'}
                  >
                    {manualRunMessages[selectedJob.id].text}
                  </p>
                )}

                <section className="jobs-schedule-section" aria-labelledby="job-schedule-title">
                  <div className="jobs-schedule-heading">
                    <div>
                      <h3 id="job-schedule-title">Schedule</h3>
                      <p className="jobs-schedule-summary">{formatInterval(selectedJob.schedule)}</p>
                    </div>
                    {jobEditorMode !== 'edit' && (
                      <div className="jobs-management-actions" role="group" aria-label="Manage selected job">
                        <button type="button" className="jobs-edit-button" onClick={openEditJob} disabled={jobMutationBusy}>Edit job</button>
                        <button
                          type="button"
                          className="jobs-edit-button"
                          onClick={() => void toggleJobEnabled(selectedJob)}
                          aria-pressed={!selectedJob.enabled}
                          disabled={jobMutationBusy}
                        >
                          {selectedJob.enabled ? 'Pause job' : 'Resume job'}
                        </button>
                        {pendingDeleteJobId !== selectedJob.id && (
                          <button
                            type="button"
                            className="jobs-edit-button jobs-delete-button"
                            onClick={() => { setPendingDeleteJobId(selectedJob.id); setJobActionError(''); setJobNotice(''); }}
                            disabled={jobMutationBusy}
                          >
                            Delete job
                          </button>
                        )}
                      </div>
                    )}
                  </div>
                  <p className="jobs-next-run"><span>Next run</span><time dateTime={selectedJob.nextRunAt}>{formatDateTime(selectedJob.nextRunAt)}</time></p>

                  {jobEditorMode === 'edit' && (
                    <JobEditorForm
                      mode="edit"
                      draft={jobDraft}
                      saving={jobMutationBusy}
                      error={jobEditorError}
                      onDraftChange={(patch) => setJobDraft((current) => ({ ...current, ...patch }))}
                      onSubmit={saveJob}
                      onCancel={cancelJobEditor}
                    />
                  )}

                  {pendingDeleteJobId === selectedJob.id && jobEditorMode !== 'edit' && (
                    <div className="jobs-delete-confirmation" role="group" aria-labelledby="job-delete-confirm-title">
                      <div>
                        <h4 id="job-delete-confirm-title">Delete {selectedJob.name}?</h4>
                        <p>The job and its saved run history will be removed. This cannot be undone.</p>
                      </div>
                      <div className="jobs-form-actions">
                        <button type="button" className="secondary-button" onClick={() => { setPendingDeleteJobId(''); setJobActionError(''); }} disabled={jobMutationBusy}>Keep job</button>
                        <button type="button" className="jobs-confirm-delete-button" onClick={() => void deleteJob(selectedJob)} disabled={jobMutationBusy}>
                          {jobMutationBusy ? 'Deleting…' : 'Delete job'}
                        </button>
                      </div>
                    </div>
                  )}
                  {jobActionError && <p className="jobs-inline-error" role="alert" aria-live="assertive">{jobActionError}</p>}
                </section>

                <section className="jobs-runs-section" aria-labelledby="job-runs-title">
                  <div className="jobs-section-heading">
                    <h3 id="job-runs-title">Recent runs</h3>
                    {runsLoading && <span role="status">Loading…</span>}
                  </div>
                  {runsError ? (
                    <div className="jobs-runs-error" role="alert">
                      <span>Run history could not be loaded: {runsError}</span>
                      <button type="button" className="jobs-edit-button" onClick={() => setJobsRevision((revision) => revision + 1)}>Retry</button>
                    </div>
                  ) : runs.length === 0 && !runsLoading ? (
                    <p className="jobs-no-runs">This job has not produced a run yet.</p>
                  ) : (
                    <ul className="jobs-run-list">
                      {runs.map((run) => (
                        <li key={run.id}>
                          <button type="button" className={`jobs-run-item${run.id === selectedRunId ? ' is-selected' : ''}`} onClick={() => selectRun(run.id)} aria-pressed={run.id === selectedRunId}>
                            <span className={`jobs-run-status status-${run.status}`}>{runStatusLabel[run.status]}</span>
                            <time dateTime={run.startedAt}>{formatDateTime(run.startedAt)}</time>
                            <span className="jobs-run-summary">{run.summary}</span>
                          </button>
                        </li>
                      ))}
                    </ul>
                  )}
                </section>

                <section className="jobs-log-section" aria-labelledby="job-output-title">
                  <div className="jobs-section-heading">
                    <div>
                      <h3 id="job-output-title">Run output</h3>
                      {selectedRun && <p>{runStatusLabel[selectedRun.status]} · started {formatDateTime(selectedRun.startedAt)}</p>}
                    </div>
                    {selectedRun && <button type="button" className="jobs-edit-button" onClick={() => setLogRetryRevision((revision) => revision + 1)} disabled={logLoading}>Refresh output</button>}
                  </div>
                  <div className="jobs-log-screen" role="region" aria-label={selectedRun ? `Output for run started ${formatDateTime(selectedRun.startedAt)}` : 'Job run output'} aria-busy={logLoading}>
                    {!selectedRun ? (
                      <div className="jobs-log-message">Select a run to view its output.</div>
                    ) : logLoading && !logLines.length ? (
                      <div className="jobs-log-message" role="status">Loading run output…</div>
                    ) : logError ? (
                      <div className="jobs-log-message jobs-log-error" role="alert">
                        <span>Run output could not be loaded: {logError}</span>
                        <button type="button" className="jobs-edit-button" onClick={() => setLogRetryRevision((revision) => revision + 1)}>Retry</button>
                      </div>
                    ) : logLines.length === 0 ? (
                      <div className="jobs-log-message">No output was recorded for this run.</div>
                    ) : (
                      <ol className="jobs-log-lines" aria-label="Most recent run output lines">
                        {visibleLogLines.map((line, index) => (
                          <li key={`${index}:${line}`}><span aria-hidden="true">{logLines.length - visibleLogLines.length + index + 1}</span><code>{line}</code></li>
                        ))}
                      </ol>
                    )}
                  </div>
                  {selectedRun && <p className="jobs-log-footnote">Showing {visibleLogLines.length} of {logLines.length} recent lines.</p>}
                </section>
              </>
            ) : (
              <div className="jobs-detail-empty">
                <h2 id="job-detail-title">Select a job</h2>
                <p>Choose a scheduled job to inspect its runs and output.</p>
              </div>
            )}
          </section>
        </div>
      )}
    </section>
  );
}
