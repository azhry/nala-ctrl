import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { Dispatch, FormEvent, RefObject, SetStateAction } from 'react';
import { ChooseFolder, CreateConfig, GetProjectDetails, RunProject, StopProject, TerminatePortOwnerAndRun } from '../wailsjs/go/main/ProjectManager';
import { EventsOn } from '../wailsjs/runtime/runtime';
import { Icon } from './icons';
import type { CreateProjectInput, ProcessIdentity, Project, ProjectDetails, ProjectLogEvent, ProjectStateEvent, WorkspaceState } from './types';
import './App.css';

type BusyAction = 'folder' | 'config' | 'run' | 'stop' | 'terminate' | null;
const projectLogEventName = 'project:log';
const projectStateEventName = 'project:state';

const emptyWorkspace: WorkspaceState = { folder: '', needsConfig: false, projects: [] };

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function folderName(path: string): string {
  const clean = path.replace(/[\\/]+$/, '');
  return clean.split(/[\\/]/).pop() || path;
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function appendLogEvent(details: ProjectDetails, event: ProjectLogEvent): ProjectDetails {
  if (event.logSize <= details.logSize) return details;
  return { ...details, logLines: [...details.logLines, event.line].slice(-250), logSize: event.logSize };
}

function App() {
  const [workspace, setWorkspace] = useState<WorkspaceState>(emptyWorkspace);
  const [selectedProject, setSelectedProject] = useState('');
  const [details, setDetails] = useState<ProjectDetails | null>(null);
  const [busyAction, setBusyAction] = useState<BusyAction>(null);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [portConflict, setPortConflict] = useState<ProcessIdentity | null>(null);
  const [conflictError, setConflictError] = useState('');
  const [followLogs, setFollowLogs] = useState(true);
  const [form, setForm] = useState<CreateProjectInput>({ name: 'backend', cwd: '.', run: 'go run .', port: '' });
  const logEndRef = useRef<HTMLDivElement>(null);

  const refreshDetails = useCallback(async (projectName: string, isCurrent: () => boolean = () => true) => {
    const next = await GetProjectDetails(projectName);
    if (!isCurrent()) return next;
    setDetails((current) => {
      if (current?.name === projectName && current.logSize > next.logSize) {
        return { ...next, logLines: current.logLines, logSize: current.logSize };
      }
      return next;
    });
    return next;
  }, []);

  const selected = useMemo(
    () => workspace.projects.find((project) => project.name === selectedProject) ?? null,
    [workspace.projects, selectedProject],
  );

  const chooseFolder = useCallback(async () => {
    setBusyAction('folder');
    setError('');
    setNotice('');
    try {
      const result = await ChooseFolder();
      if (!result) return;
      setWorkspace(result);
      setDetails(null);
      setPortConflict(null);
      setSelectedProject(result.projects[0]?.name ?? '');
      setForm({ name: 'backend', cwd: '.', run: 'go run .', port: '' });
    } catch (caught) {
      setError(errorMessage(caught));
    } finally {
      setBusyAction(null);
    }
  }, []);

  useEffect(() => {
    if (!selectedProject || workspace.needsConfig) {
      setDetails(null);
      return;
    }
    let cancelled = false;
    let latestSnapshot: ProjectDetails | null = null;
    const pendingEvents: ProjectLogEvent[] = [];
    const unsubscribe = EventsOn(projectLogEventName, (event: ProjectLogEvent) => {
      if (event.project !== selectedProject) return;
      if (!latestSnapshot) {
        pendingEvents.push(event);
        return;
      }
      latestSnapshot = appendLogEvent(latestSnapshot, event);
      setDetails((current) => {
        if (!current || current.name !== event.project) return latestSnapshot;
        return appendLogEvent(current, event);
      });
    });
    const unsubscribeState = EventsOn(projectStateEventName, (event: ProjectStateEvent) => {
      if (event.project !== selectedProject) return;
      void refreshDetails(selectedProject, () => !cancelled).then(() => {
        if (!cancelled) setError('');
      }).catch((caught) => {
        if (!cancelled) setError(errorMessage(caught));
      });
    });
    void refreshDetails(selectedProject, () => !cancelled).then((initial) => {
      if (cancelled) return;
      latestSnapshot = pendingEvents
        .filter((event) => event.logSize > initial.logSize)
        .sort((left, right) => left.logSize - right.logSize)
        .reduce(appendLogEvent, initial);
      pendingEvents.length = 0;
      setDetails(latestSnapshot);
      setError('');
    }).catch((caught) => {
      if (!cancelled) setError(errorMessage(caught));
    });
    return () => {
      cancelled = true;
      unsubscribe();
      unsubscribeState();
    };
  }, [selectedProject, workspace.folder, workspace.needsConfig, refreshDetails]);

  useEffect(() => {
    if (followLogs && details?.logLines.length) {
      logEndRef.current?.scrollIntoView({ block: 'end', behavior: 'smooth' });
    }
  }, [details?.logLines, followLogs]);

  useEffect(() => {
    if (!notice) return;
    const timer = window.setTimeout(() => setNotice(''), 3200);
    return () => window.clearTimeout(timer);
  }, [notice]);

  const createConfig = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setBusyAction('config');
    setError('');
    try {
      const next = await CreateConfig(form);
      setWorkspace(next);
      setSelectedProject(next.projects[0]?.name ?? '');
      setNotice('nala.yml created');
    } catch (caught) {
      setError(errorMessage(caught));
    } finally {
      setBusyAction(null);
    }
  };

  const runProject = async () => {
    if (!selectedProject) return;
    setBusyAction('run');
    setError('');
    setNotice('');
    try {
      const result = await RunProject(selectedProject);
      if (result.portOwner) {
        setConflictError('');
        setPortConflict(result.portOwner);
      } else if (result.started) {
        setNotice(`Started ${selectedProject}`);
        await refreshDetails(selectedProject);
      }
    } catch (caught) {
      const message = errorMessage(caught);
      if (message.toLowerCase().includes('already running')) {
        try {
          const current = await refreshDetails(selectedProject);
          if (current.running) {
            setError('');
            setNotice(`${selectedProject} is already running`);
            return;
          }
        } catch (refreshError) {
          setError(errorMessage(refreshError));
          return;
        }
      }
      setError(message);
    } finally {
      setBusyAction(null);
    }
  };

  const terminateAndRun = async () => {
    if (!selectedProject || !portConflict) return;
    setBusyAction('terminate');
    setConflictError('');
    try {
      const result = await TerminatePortOwnerAndRun(selectedProject, portConflict.pid, portConflict.createTime);
      if (result.started) {
        setPortConflict(null);
        setNotice(`Started ${selectedProject}`);
        await refreshDetails(selectedProject);
      } else if (result.portOwner) {
        setPortConflict(result.portOwner);
      }
    } catch (caught) {
      setConflictError(errorMessage(caught));
    } finally {
      setBusyAction(null);
    }
  };

  const stopProject = async () => {
    if (!selectedProject) return;
    setBusyAction('stop');
    setError('');
    try {
      await StopProject(selectedProject);
      await refreshDetails(selectedProject);
      setNotice(`Stopped ${selectedProject}`);
    } catch (caught) {
      setError(errorMessage(caught));
    } finally {
      setBusyAction(null);
    }
  };

  const selectProject = (project: Project) => {
    if (project.name === selectedProject) {
      setError('');
      setNotice('');
      void refreshDetails(project.name).catch((caught) => setError(errorMessage(caught)));
      return;
    }
    setSelectedProject(project.name);
    setDetails(null);
    setError('');
    setNotice('');
  };

  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="brand-lockup" aria-label="Nala Ctrl">
          <div className="brand-mark"><span /></div>
          <div className="brand-type"><strong>nala<span>ctrl</span></strong><small>LOCAL PROCESS DESK</small></div>
        </div>

        <div className="workspace-label">WORKSPACE</div>
        <button className="workspace-card" onClick={chooseFolder} disabled={busyAction === 'folder'} title={workspace.folder || 'Choose a project folder'}>
          <span className="workspace-card-icon"><Icon name="folder" /></span>
          <span className="workspace-card-copy">
            <strong>{workspace.folder ? folderName(workspace.folder) : 'Choose a folder'}</strong>
            <small>{workspace.folder ? 'Click to switch workspace' : 'Select a folder with nala.yml'}</small>
          </span>
          <Icon className="workspace-chevron" name="chevron" />
        </button>
        {workspace.folder && <div className="workspace-path" title={workspace.folder}>{workspace.folder}</div>}

        <div className="project-nav-head">
          <span>PROJECTS</span>
          {workspace.projects.length > 0 && <span className="project-count">{workspace.projects.length}</span>}
        </div>
        <nav className="project-list" aria-label="Configured projects">
          {workspace.projects.map((project) => {
            const active = project.name === selectedProject;
            const live = active && details?.running;
            return (
              <button key={project.name} className={`project-item${active ? ' selected' : ''}`} onClick={() => selectProject(project)} aria-current={active ? 'page' : undefined}>
                <span className={`project-dot${live ? ' live' : ''}`} />
                <span className="project-item-copy"><strong>{project.name}</strong><small>{project.port ? `PORT ${project.port}` : 'NO PORT CHECK'}</small></span>
                {active && <Icon className="project-chevron" name="chevron" />}
              </button>
            );
          })}
          {!workspace.projects.length && workspace.folder && !workspace.needsConfig && <div className="nav-empty">No projects in this config.</div>}
          {!workspace.folder && <div className="nav-empty">Your configured projects will appear here.</div>}
        </nav>

        <div className="sidebar-bottom">
          <span className="local-indicator"><i /> LOCAL MACHINE</span>
          <span className="sidebar-footnote">Only projects in the selected nala.yml are managed.</span>
        </div>
      </aside>

      <main className="main-area">
        <header className="topbar">
          <div className="breadcrumb">
            <span>WORKSPACE</span><Icon name="chevron" /><strong>{workspace.folder ? folderName(workspace.folder) : 'No folder selected'}</strong>
            {selected && <><Icon name="chevron" /><span className="crumb-project">{selected.name}</span></>}
          </div>
          <div className="topbar-actions">
            {workspace.folder && <span className="local-pill"><i /> This machine</span>}
            <button className="icon-button" onClick={chooseFolder} title="Choose another folder" aria-label="Choose another folder" disabled={busyAction === 'folder'}><Icon name="folder" /></button>
          </div>
        </header>

        <div className="content-scroll">
          {error && <div className="message-banner error-banner" role="alert"><Icon name="warning" /><span>{error}</span><button onClick={() => setError('')} aria-label="Dismiss error"><Icon name="close" /></button></div>}
          {notice && <div className="message-banner notice-banner" role="status"><Icon name="check" /><span>{notice}</span><button onClick={() => setNotice('')} aria-label="Dismiss notification"><Icon name="close" /></button></div>}

          {!workspace.folder && <WelcomeScreen onChoose={chooseFolder} busy={busyAction === 'folder'} />}
          {workspace.folder && workspace.needsConfig && (
            <SetupScreen folder={workspace.folder} form={form} setForm={setForm} onSubmit={createConfig} busy={busyAction === 'config'} />
          )}
          {workspace.folder && !workspace.needsConfig && workspace.projects.length === 0 && (
            <div className="empty-project-state">
              <div className="empty-glyph"><Icon name="server" /></div>
              <p className="eyebrow">NOTHING TO MANAGE</p>
              <h1>No projects in nala.yml</h1>
              <p>Add a project with <code>cwd</code> and <code>run</code>, then reopen this folder.</p>
              <button className="secondary-button" onClick={chooseFolder}><Icon name="refresh" /> Reopen folder</button>
            </div>
          )}
          {selected && !workspace.needsConfig && (
            <ProjectScreen
              project={selected}
              details={details}
              busyAction={busyAction}
              followLogs={followLogs}
              logEndRef={logEndRef}
              onRun={runProject}
              onStop={stopProject}
              onRefresh={() => void refreshDetails(selected.name).catch((caught) => setError(errorMessage(caught)))}
              onToggleFollow={() => setFollowLogs((value) => !value)}
            />
          )}
        </div>
      </main>

      {portConflict && selected && (
        <PortConflictDialog
          project={selected}
          owner={portConflict}
          error={conflictError}
          busy={busyAction === 'terminate'}
          onCancel={() => { setPortConflict(null); setConflictError(''); }}
          onConfirm={terminateAndRun}
        />
      )}
    </div>
  );
}

function WelcomeScreen({ onChoose, busy }: { onChoose: () => void; busy: boolean }) {
  return (
    <section className="welcome-screen">
      <div className="welcome-grid" aria-hidden="true" />
      <div className="welcome-content">
        <div className="welcome-emblem"><Icon name="terminal" /><span className="emblem-signal" /></div>
        <p className="eyebrow">A CLEAR VIEW OF WHAT'S RUNNING</p>
        <h1>Your projects,<br /><em>under control.</em></h1>
        <p className="welcome-copy">Choose a project folder to see its configured services, follow their output, and start or stop them from one quiet place.</p>
        <button className="primary-button welcome-button" onClick={onChoose} disabled={busy}>
          <Icon name="folder-plus" /> {busy ? 'Opening folder…' : 'Choose project folder'} <Icon className="button-arrow" name="arrow" />
        </button>
        <div className="welcome-note"><span className="note-line" /> Looks for <code>nala.yml</code> at the folder root</div>
      </div>
      <div className="welcome-footer"><span>01 / SELECT WORKSPACE</span><span>LOCAL · PRIVATE · SIMPLE</span></div>
    </section>
  );
}

function SetupScreen({
  folder, form, setForm, onSubmit, busy,
}: {
  folder: string;
  form: CreateProjectInput;
  setForm: Dispatch<SetStateAction<CreateProjectInput>>;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
  busy: boolean;
}) {
  const update = (key: keyof CreateProjectInput, value: string) => setForm((current) => ({ ...current, [key]: value }));
  return (
    <section className="setup-layout">
      <div className="setup-intro">
        <div className="setup-icon"><Icon name="folder-plus" /></div>
        <p className="eyebrow">FIRST RUN</p>
        <h1>Give this workspace<br />a starting point.</h1>
        <p>There’s no <code>nala.yml</code> here yet. Add one project to create the config. You can add more projects to the same file later.</p>
        <div className="setup-location"><Icon name="folder" /><span>{folder}</span></div>
      </div>

      <form className="setup-card" onSubmit={onSubmit}>
        <div className="setup-card-head"><div><span className="eyebrow">NEW CONFIGURATION</span><h2>First project</h2></div><span className="config-file-badge">nala.yml</span></div>
        <label className="field-label" htmlFor="project-name">Project name</label>
        <input id="project-name" className="text-field" value={form.name} onChange={(event) => update('name', event.target.value)} placeholder="backend" pattern="[A-Za-z0-9][A-Za-z0-9_-]{0,63}" required autoComplete="off" />
        <label className="field-label" htmlFor="project-cwd">Working directory</label>
        <input id="project-cwd" className="text-field mono-field" value={form.cwd} onChange={(event) => update('cwd', event.target.value)} placeholder="." required autoComplete="off" />
        <p className="field-help">Relative to the folder you chose, or an absolute path.</p>
        <label className="field-label" htmlFor="project-run">Run command</label>
        <input id="project-run" className="text-field mono-field" value={form.run} onChange={(event) => update('run', event.target.value)} placeholder="go run ." required autoComplete="off" />
        <label className="field-label" htmlFor="project-port">Port <span>OPTIONAL</span></label>
        <input id="project-port" className="text-field mono-field" value={form.port} onChange={(event) => update('port', event.target.value)} placeholder="Auto-detect from command" type="number" min="1" max="65535" />
        <p className="field-help">A clear port flag or common dev-server default is detected when this is blank.</p>
        <div className="config-preview"><span>CREATES</span><code>projects.{form.name || 'project'}.cwd · run · port</code></div>
        <button className="primary-button setup-submit" type="submit" disabled={busy}>{busy ? 'Creating config…' : 'Create nala.yml'}<Icon name="arrow" /></button>
      </form>
    </section>
  );
}

function ProjectScreen({
  project, details, busyAction, followLogs, logEndRef, onRun, onStop, onRefresh, onToggleFollow,
}: {
  project: Project;
  details: ProjectDetails | null;
  busyAction: BusyAction;
  followLogs: boolean;
  logEndRef: RefObject<HTMLDivElement | null>;
  onRun: () => void;
  onStop: () => void;
  onRefresh: () => void;
  onToggleFollow: () => void;
}) {
  const running = details?.running ?? false;
  const owner = details?.portOwner ?? null;
  const lines = details?.logLines ?? [];
  const isBusy = busyAction === 'run' || busyAction === 'stop';
  return (
    <section className="project-screen">
      <div className="project-heading">
        <div className="project-heading-copy">
          <p className="eyebrow">CONFIGURED PROJECT</p>
          <div className="project-title-row"><span className={`large-status-dot${running ? ' live' : ''}`} /><h1>{project.name}</h1></div>
          <p className="run-command"><Icon name="terminal" /><code>{project.run}</code><span>in</span><code className="cwd-inline">{project.cwd}</code></p>
        </div>
        <div className="project-actions">
          <button className="icon-button refresh-button" onClick={onRefresh} title="Refresh project" aria-label="Refresh project"><Icon name="refresh" /></button>
          {running ? (
            <button className="stop-button" onClick={onStop} disabled={isBusy}><Icon name="stop" />{busyAction === 'stop' ? 'Stopping…' : 'Stop project'}</button>
          ) : (
            <button className="run-button" onClick={onRun} disabled={isBusy}><Icon name="play" />{busyAction === 'run' ? 'Starting…' : 'Run project'}</button>
          )}
        </div>
      </div>

      <div className="summary-grid">
        <article className={`summary-card status-card${running ? ' is-running' : ''}`}>
          <div className="summary-label"><span>PROCESS</span><Icon name="server" /></div>
          <div className="summary-value">{details ? (running ? 'Running' : 'Stopped') : 'Checking'}</div>
          <div className="summary-meta">{running ? `PID ${details?.pid}` : owner ? `${owner.name} holds the port` : 'Managed by Nala Ctrl'}</div>
          <div className={`card-signal${running ? ' active' : ''}`}><i /><i /><i /><i /><i /><i /><i /><i /><i /></div>
        </article>
        <article className="summary-card">
          <div className="summary-label"><span>PORT</span><span className={`port-state${owner ? ' occupied' : ''}`}><i />{owner ? 'IN USE' : project.port ? 'AVAILABLE' : 'NOT SET'}</span></div>
          <div className="summary-value">{project.port ? `:${project.port}` : '—'}</div>
          <div className="summary-meta">{owner ? `${owner.name} · PID ${owner.pid}` : project.port ? 'Ready for this project' : 'No port check configured'}</div>
        </article>
        <article className="summary-card cwd-card">
          <div className="summary-label"><span>WORKING DIRECTORY</span><Icon name="folder" /></div>
          <div className="cwd-value" title={project.cwd}>{project.cwd}</div>
          <div className="summary-meta">Output → {project.logPath}</div>
        </article>
      </div>

      <section className="log-panel" aria-label={`${project.name} log stream`}>
        <div className="log-panel-head">
          <div className="log-title"><span className="log-live-mark"><i /></span><div><h2>Output stream</h2><p>{details?.logPath ?? project.logPath}</p></div></div>
          <div className="log-controls">
            <span className="log-count">{details ? `${lines.length} lines · ${formatBytes(details.logSize)}` : 'Loading log…'}</span>
            <button className={`follow-button${followLogs ? ' following' : ''}`} onClick={onToggleFollow} aria-pressed={followLogs}><span className="follow-dot" />{followLogs ? 'Following' : 'Follow logs'}</button>
          </div>
        </div>
        <div className="log-screen" role="log" aria-live="off" aria-relevant="additions text">
          {lines.length ? lines.map((line, index) => (
            <div className="log-line" key={`${index}-${line}`}><span className="line-number">{String(index + 1).padStart(3, '0')}</span><span className="line-text">{line || ' '}</span></div>
          )) : (
            <div className="log-empty"><div className="log-empty-mark"><Icon name="terminal" /></div><strong>No output yet</strong><span>{running ? 'Waiting for the process to write its first line.' : 'Run this project to start capturing its output.'}</span></div>
          )}
          <div ref={logEndRef} />
        </div>
        <div className="log-panel-foot"><span><i className={running ? 'foot-live' : ''} />{running ? 'STREAM ACTIVE' : 'STREAM IDLE'}</span><span>Live Wails events · last 250 lines</span></div>
      </section>
    </section>
  );
}

function PortConflictDialog({
  project, owner, error, busy, onCancel, onConfirm,
}: {
  project: Project;
  owner: ProcessIdentity;
  error: string;
  busy: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => { if (event.key === 'Escape' && !busy) onCancel(); };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [busy, onCancel]);

  return (
    <div className="dialog-scrim" onMouseDown={(event) => { if (event.target === event.currentTarget && !busy) onCancel(); }}>
      <section className="conflict-dialog" role="dialog" aria-modal="true" aria-labelledby="conflict-title">
        <div className="dialog-topline"><span className="warning-icon"><Icon name="warning" /></span><button className="dialog-close" onClick={onCancel} aria-label="Cancel" disabled={busy}><Icon name="close" /></button></div>
        <p className="eyebrow">PORT CONFLICT</p>
        <h2 id="conflict-title">Port {project.port} is already in use.</h2>
        <p className="dialog-copy">Nala Ctrl found a process listening on this port. Stop this process and run <strong>{project.name}</strong>?</p>
        <div className="process-identity">
          <div className="process-avatar"><Icon name="terminal" /></div>
          <div className="process-copy"><strong>{owner.name}</strong><span>{owner.managedProject ? `Managed project: ${owner.managedProject}` : 'Existing process on this machine'}</span></div>
          <div className="process-pid"><span>PID</span><strong>{owner.pid}</strong></div>
        </div>
        {!owner.canTerminate && <div className="cannot-terminate"><Icon name="warning" />{owner.terminationNote || 'This process cannot be terminated safely.'}</div>}
        {error && <div className="dialog-error" role="alert">{error}</div>}
        <div className="dialog-actions"><button className="secondary-button" onClick={onCancel} disabled={busy}>Cancel</button><button className="danger-button" onClick={onConfirm} disabled={busy || !owner.canTerminate}><Icon name="stop" />{busy ? 'Stopping process…' : 'Stop process & run'}</button></div>
        <p className="dialog-footnote">The process identity is checked again before it is stopped.</p>
      </section>
    </div>
  );
}

export default App;
