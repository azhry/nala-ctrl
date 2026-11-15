import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { GetJobs, GetJobRunLog, GetJobRuns, RunJobNow, UpdateJobSchedule } from '../wailsjs/go/main/ProjectManager';
import { Icon } from './icons';
import type { Job, JobRun, JobRunStatus, JobSchedule } from './types';
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
type JobBinding = Omit<Job, 'lastRun'> & { lastRun?: JobRunBinding };
type ManualRunMessage = { kind: 'status' | 'error'; text: string };

const supportedRunStatuses = new Set<string>(['running', 'healthy', 'unhealthy', 'failed']);

function normalizeRun(run: JobRunBinding): JobRun {
  if (!supportedRunStatuses.has(run.status)) throw new Error(`Unsupported job run status: ${run.status}`);
  return { ...run, status: run.status as JobRunStatus };
}

function normalizeJob(job: JobBinding): Job {
  return { ...job, lastRun: job.lastRun ? normalizeRun(job.lastRun) : undefined };
}

const runStatusLabel: Record<JobRun['status'], string> = {
  running: 'Running',
  healthy: 'Healthy',
  unhealthy: 'Unhealthy',
  failed: 'Failed',
};

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

  const [editingSchedule, setEditingSchedule] = useState(false);
  const [scheduleDraft, setScheduleDraft] = useState({ intervalMinutes: '', offsetMinutes: '' });
  const [scheduleSaving, setScheduleSaving] = useState(false);
  const [scheduleError, setScheduleError] = useState('');
  const [scheduleNotice, setScheduleNotice] = useState('');
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
        setEditingSchedule(false);
        setScheduleError('');
        setScheduleNotice('');
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
    if (jobID === selectedJobId || scheduleSaving) return;
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
    setEditingSchedule(false);
    setScheduleError('');
    setScheduleNotice('');
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

  const startScheduleEdit = () => {
    if (!selectedJob) return;
    setScheduleDraft({
      intervalMinutes: String(selectedJob.schedule.intervalMinutes),
      offsetMinutes: String(selectedJob.schedule.offsetMinutes),
    });
    setScheduleError('');
    setScheduleNotice('');
    setEditingSchedule(true);
  };

  const saveSchedule = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!selectedJob) return;
    const intervalMinutes = Number(scheduleDraft.intervalMinutes);
    const offsetMinutes = Number(scheduleDraft.offsetMinutes);
    if (!Number.isInteger(intervalMinutes) || intervalMinutes < 1 || intervalMinutes > 1440) {
      setScheduleError('Enter an interval from 1 to 1440 whole minutes.');
      return;
    }
    if (!Number.isInteger(offsetMinutes) || offsetMinutes < 0 || offsetMinutes >= intervalMinutes) {
      setScheduleError('Offset must be a whole number from zero to one less than the interval.');
      return;
    }

    const jobID = selectedJob.id;
    setScheduleSaving(true);
    setScheduleError('');
    setScheduleNotice('');
    try {
      const updated = normalizeJob(await UpdateJobSchedule(jobID, { intervalMinutes, offsetMinutes }));
      jobsRequestRef.current += 1;
      setJobsLoading(false);
      setJobsRefreshing(false);
      setJobs((current) => current.map((job) => job.id === updated.id ? updated : job));
      if (selectedJobIdRef.current === jobID) {
        setEditingSchedule(false);
        setScheduleNotice('Schedule saved. The next run time has been updated.');
      }
    } catch (caught) {
      if (selectedJobIdRef.current === jobID) {
        setScheduleError(caught instanceof Error ? caught.message : String(caught));
      }
    } finally {
      setScheduleSaving(false);
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
          <p>See what runs next, inspect recent results, and adjust a schedule.</p>
        </div>
        <button type="button" className="secondary-button jobs-refresh-button" onClick={() => void refreshJobs(true)} disabled={jobsLoading || jobsRefreshing}>
          <Icon name="refresh" />{jobsLoading ? 'Loading jobs…' : jobsRefreshing ? 'Refreshing…' : 'Refresh jobs'}
        </button>
      </header>

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
          <p>No jobs are registered with this desktop app yet.</p>
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
                    disabled={scheduleSaving}
                  >
                    <span className="jobs-time-mark" aria-hidden="true" />
                    <time className="jobs-next-time" dateTime={job.nextRunAt} title={formatDateTime(job.nextRunAt)}>{formatClock(job.nextRunAt)}</time>
                    <span className="jobs-timeline-copy">
                      <strong>{job.name}</strong>
                      <small>{job.description || formatInterval(job.schedule)}</small>
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
                    {selectedJob.description && <p>{selectedJob.description}</p>}
                  </div>
                  <button
                    type="button"
                    className="jobs-run-now-button"
                    onClick={() => void runJobNow(selectedJob)}
                    disabled={pendingManualRunIds.has(selectedJob.id) || scheduleSaving}
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
                    {!editingSchedule && <button type="button" className="jobs-edit-button" onClick={startScheduleEdit}>Edit schedule</button>}
                  </div>
                  <p className="jobs-next-run"><span>Next run</span><time dateTime={selectedJob.nextRunAt}>{formatDateTime(selectedJob.nextRunAt)}</time></p>

                  {editingSchedule && (
                    <form className="jobs-schedule-form" onSubmit={saveSchedule}>
                      <div className="jobs-schedule-fields">
                        <label>
                          Interval <span>(minutes)</span>
                          <input
                            type="number"
                            min="1"
                            max="1440"
                            step="1"
                            required
                            value={scheduleDraft.intervalMinutes}
                            onChange={(event) => setScheduleDraft((current) => ({ ...current, intervalMinutes: event.target.value }))}
                            aria-describedby="job-schedule-hint"
                            disabled={scheduleSaving}
                          />
                        </label>
                        <label>
                          Offset <span>(minutes)</span>
                          <input
                            type="number"
                            min="0"
                            max={Math.max(0, (Number(scheduleDraft.intervalMinutes) || 1) - 1)}
                            step="1"
                            required
                            value={scheduleDraft.offsetMinutes}
                            onChange={(event) => setScheduleDraft((current) => ({ ...current, offsetMinutes: event.target.value }))}
                            aria-describedby="job-schedule-hint"
                            disabled={scheduleSaving}
                          />
                        </label>
                      </div>
                      <p id="job-schedule-hint" className="jobs-form-hint">Use 1–1440 whole minutes; offset must be less than the interval.</p>
                      {scheduleError && <p className="jobs-inline-error" role="alert">{scheduleError}</p>}
                      <div className="jobs-form-actions">
                        <button type="button" className="secondary-button" onClick={() => { setEditingSchedule(false); setScheduleError(''); }} disabled={scheduleSaving}>Cancel</button>
                        <button type="submit" className="primary-button jobs-save-button" disabled={scheduleSaving}>{scheduleSaving ? 'Saving…' : 'Save schedule'}</button>
                      </div>
                    </form>
                  )}
                  {scheduleNotice && <p className="jobs-inline-success" role="status">{scheduleNotice}</p>}
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
