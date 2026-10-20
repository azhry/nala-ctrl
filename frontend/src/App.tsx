import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { Dispatch, FormEvent, RefObject, SetStateAction } from 'react';
import { ChooseFolder, CreateConfig, GetProjectDetails, GetWorkspace, RunProject, StopProject, TerminatePortOwnerAndRun } from '../wailsjs/go/main/ProjectManager';
import { EventsOn } from '../wailsjs/runtime/runtime';
import { Icon } from './icons';
import type { CreateProjectInput, ProcessIdentity, Project, ProjectDetails, ProjectLogEvent, ProjectStateEvent, WorkspaceState } from './types';
import './App.css';

type GlobalBusyAction = 'folder' | 'config' | null;
type ServiceAction = 'run' | 'stop' | 'terminate';
type ServiceStatus = { running: boolean; pid: number };
const projectLogEventName = 'project:log';
const projectStateEventName = 'project:state';

const emptyWorkspace: WorkspaceState = { folder: '', needsConfig: false, projects: [] };

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function workspaceWarningMessages(workspace: WorkspaceState): string[] {
  const warnings = (workspace as WorkspaceState & { warnings?: unknown }).warnings;
  return Array.isArray(warnings) ? warnings.filter((warning): warning is string => typeof warning === 'string') : [];
}

function folderName(path: string): string {
  const clean = path.replace(/[\\/]+$/, '');
  return clean.split(/[\\/]/).pop() || path;
}

function displayFolderPath(path: string): string {
  const clean = path.replace(/[\\/]+$/, '');
  const parts = clean.split(/[\\/]/).filter(Boolean);
  const separator = clean.includes('\\') ? '\\' : '/';
  const suffix = parts.slice(-3).join(separator);
  return parts.length > 3 ? `…${separator}${suffix}` : path;
}

function sameFolder(left: string, right: string): boolean {
  const normalize = (path: string) => path.replace(/[\\/]+$/, '').replace(/\\/g, '/');
  const normalizedLeft = normalize(left);
  const normalizedRight = normalize(right);
  if (normalizedLeft === normalizedRight) return true;
  return /^[a-z]:\//i.test(normalizedLeft) && /^[a-z]:\//i.test(normalizedRight)
    && normalizedLeft.toLowerCase() === normalizedRight.toLowerCase();
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
  const [workspaceHydrating, setWorkspaceHydrating] = useState(true);
  const [workspaceWarnings, setWorkspaceWarnings] = useState<string[]>([]);
  const [selectedServiceId, setSelectedServiceId] = useState('');
  const selectedServiceRef = useRef('');
  const workspaceHydrationRef = useRef<Promise<WorkspaceState> | null>(null);
  const workspaceChangeVersion = useRef(0);
  const [setupOpen, setSetupOpen] = useState(false);
  const [serviceStatuses, setServiceStatuses] = useState<Record<string, ServiceStatus>>({});
  const [serviceActions, setServiceActions] = useState<Record<string, ServiceAction>>({});
  const serviceStateVersions = useRef<Record<string, number>>({});
  const [details, setDetails] = useState<ProjectDetails | null>(null);
  const [busyAction, setBusyAction] = useState<GlobalBusyAction>(null);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [portConflict, setPortConflict] = useState<ProcessIdentity | null>(null);
  const [portConflictServiceId, setPortConflictServiceId] = useState('');
  const [conflictError, setConflictError] = useState('');
  const [followLogs, setFollowLogs] = useState(true);
  const [form, setForm] = useState<CreateProjectInput>({ cwd: '.', run: 'go run .', port: '' });
  const logScreenRef = useRef<HTMLDivElement>(null);

  const refreshDetails = useCallback(async (serviceId: string, isCurrent: () => boolean = () => selectedServiceRef.current === serviceId) => {
    const stateVersion = serviceStateVersions.current[serviceId] ?? 0;
    const next = await GetProjectDetails(serviceId);
    if ((serviceStateVersions.current[serviceId] ?? 0) === stateVersion) {
      setServiceStatuses((current) => ({ ...current, [serviceId]: { running: next.running, pid: next.pid } }));
    }
    if (!isCurrent()) return next;
    setDetails((current) => {
      if (current?.serviceId === serviceId && current.logSize > next.logSize) {
        return { ...next, logLines: current.logLines, logSize: current.logSize };
      }
      return next;
    });
    return next;
  }, []);

  const serviceEntries = useMemo(
    () => workspace.projects.flatMap((project) => project.services.map((service) => ({ project, service }))),
    [workspace.projects],
  );
  const selectedEntry = useMemo(
    () => serviceEntries.find((entry) => entry.service.id === selectedServiceId) ?? null,
    [serviceEntries, selectedServiceId],
  );
  const selectedProject = selectedEntry?.project ?? null;
  const selected = selectedEntry?.service ?? null;

  const selectServiceId = useCallback((serviceId: string) => {
    selectedServiceRef.current = serviceId;
    setSelectedServiceId(serviceId);
  }, []);

  useEffect(() => {
    let cancelled = false;
    const version = workspaceChangeVersion.current;
    workspaceHydrationRef.current ??= Promise.resolve().then(() => GetWorkspace());

    void workspaceHydrationRef.current.then((restored) => {
      if (cancelled || workspaceChangeVersion.current !== version) return;
      const firstServiceId = restored.projects.flatMap((project) => project.services)[0]?.id ?? '';
      setWorkspace(restored);
      setWorkspaceWarnings(workspaceWarningMessages(restored));
      setSetupOpen(restored.needsConfig && !firstServiceId);
      selectServiceId(firstServiceId);
    }).catch((caught) => {
      if (cancelled || workspaceChangeVersion.current !== version) return;
      setError(`Unable to restore saved projects: ${errorMessage(caught)}`);
    }).finally(() => {
      if (!cancelled) setWorkspaceHydrating(false);
    });

    return () => { cancelled = true; };
  }, [selectServiceId]);

  const setServiceAction = (serviceId: string, action: ServiceAction | null) => {
    setServiceActions((current) => {
      const next = { ...current };
      if (action) next[serviceId] = action;
      else delete next[serviceId];
      return next;
    });
  };

  const chooseFolder = useCallback(async () => {
    setBusyAction('folder');
    setError('');
    setNotice('');
    try {
      const result = await ChooseFolder();
      if (!result) return;
      workspaceChangeVersion.current += 1;
      setWorkspace(result);
      setWorkspaceWarnings(workspaceWarningMessages(result));
      setSetupOpen(result.needsConfig);
      setPortConflict(null);
      setPortConflictServiceId('');
      setConflictError('');
      const previousServiceId = selectedServiceRef.current;
      const addedProject = result.projects.find((project) => sameFolder(project.folder, result.folder) || sameFolder(project.id, result.folder));
      const addedProjectServiceIds = addedProject?.services.map((service) => service.id) ?? [];
      const serviceStillExists = result.projects.some((project) => project.services.some((service) => service.id === previousServiceId));
      const firstServiceId = result.projects.flatMap((project) => project.services)[0]?.id ?? '';
      let nextServiceId = serviceStillExists ? previousServiceId : firstServiceId;
      if (result.needsConfig) {
        nextServiceId = '';
        setForm({ cwd: '.', run: 'go run .', port: '' });
      } else if (addedProjectServiceIds.length) {
        nextServiceId = addedProjectServiceIds.includes(previousServiceId) ? previousServiceId : addedProjectServiceIds[0];
      }
      if (nextServiceId !== selectedServiceRef.current) setDetails(null);
      selectServiceId(nextServiceId);
      if (nextServiceId && nextServiceId === previousServiceId) {
        void refreshDetails(nextServiceId).catch((caught) => setError(errorMessage(caught)));
      }
    } catch (caught) {
      setError(errorMessage(caught));
    } finally {
      setBusyAction(null);
    }
  }, [refreshDetails, selectServiceId]);

  useEffect(() => EventsOn(projectStateEventName, (event: ProjectStateEvent) => {
    serviceStateVersions.current[event.serviceId] = (serviceStateVersions.current[event.serviceId] ?? 0) + 1;
    setServiceStatuses((current) => ({ ...current, [event.serviceId]: { running: event.running, pid: event.pid } }));
  }), []);

  useEffect(() => {
    if (!selected) {
      setDetails(null);
      return;
    }
    const serviceId = selected.id;
    let cancelled = false;
    let latestSnapshot: ProjectDetails | null = null;
    const pendingEvents: ProjectLogEvent[] = [];
    const unsubscribe = EventsOn(projectLogEventName, (event: ProjectLogEvent) => {
      if (cancelled || selectedServiceRef.current !== serviceId || event.serviceId !== serviceId) return;
      if (!latestSnapshot) {
        pendingEvents.push(event);
        return;
      }
      latestSnapshot = appendLogEvent(latestSnapshot, event);
      setDetails((current) => {
        if (!current || current.serviceId !== event.serviceId) return current;
        return appendLogEvent(current, event);
      });
    });
    const unsubscribeState = EventsOn(projectStateEventName, (event: ProjectStateEvent) => {
      if (cancelled || selectedServiceRef.current !== serviceId || event.serviceId !== serviceId) return;
      void refreshDetails(serviceId, () => !cancelled && selectedServiceRef.current === serviceId).then(() => {
        if (!cancelled) setError('');
      }).catch((caught) => {
        if (!cancelled) setError(errorMessage(caught));
      });
    });
    void refreshDetails(serviceId, () => !cancelled && selectedServiceRef.current === serviceId).then((initial) => {
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
  }, [selected?.id, refreshDetails]);

  useEffect(() => {
    const logScreen = logScreenRef.current;
    if (followLogs && logScreen && details?.logLines.length) {
      logScreen.scrollTo({ top: logScreen.scrollHeight, behavior: 'smooth' });
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
      workspaceChangeVersion.current += 1;
      setWorkspace(next);
      setWorkspaceWarnings(workspaceWarningMessages(next));
      setSetupOpen(false);
      const createdProject = next.projects.find((project) => sameFolder(project.folder, next.folder) || sameFolder(project.id, next.folder));
      const nextServiceId = createdProject?.services[0]?.id ?? next.projects.flatMap((project) => project.services)[0]?.id ?? '';
      if (nextServiceId !== selectedServiceRef.current) setDetails(null);
      selectServiceId(nextServiceId);
      setNotice('nala.yml created');
    } catch (caught) {
      setError(errorMessage(caught));
    } finally {
      setBusyAction(null);
    }
  };

  const runService = async () => {
    if (!selected) return;
    const serviceId = selected.id;
    const serviceName = selected.name;
    const projectName = selectedProject?.name ?? 'project';
    setServiceAction(serviceId, 'run');
    setError('');
    setNotice('');
    try {
      const result = await RunProject(serviceId);
      if (result.portOwner) {
        if (selectedServiceRef.current === serviceId) {
          setConflictError('');
          setPortConflictServiceId(serviceId);
          setPortConflict(result.portOwner);
        }
      } else if (result.started) {
        if (selectedServiceRef.current === serviceId) setNotice(`Started ${projectName} / ${serviceName}`);
        await refreshDetails(serviceId);
      }
    } catch (caught) {
      const message = errorMessage(caught);
      if (message.toLowerCase().includes('already running')) {
        try {
          const current = await refreshDetails(serviceId);
          if (current.running) {
            if (selectedServiceRef.current === serviceId) {
              setError('');
              setNotice(`${projectName} / ${serviceName} is already running`);
            }
            return;
          }
        } catch (refreshError) {
          if (selectedServiceRef.current === serviceId) setError(errorMessage(refreshError));
          return;
        }
      }
      if (selectedServiceRef.current === serviceId) setError(message);
    } finally {
      setServiceAction(serviceId, null);
    }
  };

  const terminateAndRun = async () => {
    if (!selected || !portConflict || selected.id !== portConflictServiceId) return;
    const serviceId = selected.id;
    const serviceName = selected.name;
    const projectName = selectedProject?.name ?? 'project';
    setServiceAction(serviceId, 'terminate');
    setConflictError('');
    try {
      const result = await TerminatePortOwnerAndRun(serviceId, portConflict.pid, portConflict.createTime);
      if (result.started) {
        if (selectedServiceRef.current === serviceId) {
          setPortConflict(null);
          setPortConflictServiceId('');
          setNotice(`Started ${projectName} / ${serviceName}`);
        }
        await refreshDetails(serviceId);
      } else if (result.portOwner) {
        if (selectedServiceRef.current === serviceId) setPortConflict(result.portOwner);
      }
    } catch (caught) {
      if (selectedServiceRef.current === serviceId) setConflictError(errorMessage(caught));
    } finally {
      setServiceAction(serviceId, null);
    }
  };

  const stopService = async () => {
    if (!selected) return;
    const serviceId = selected.id;
    const serviceName = selected.name;
    const projectName = selectedProject?.name ?? 'project';
    setServiceAction(serviceId, 'stop');
    setError('');
    try {
      await StopProject(serviceId);
      await refreshDetails(serviceId);
      if (selectedServiceRef.current === serviceId) setNotice(`Stopped ${projectName} / ${serviceName}`);
    } catch (caught) {
      if (selectedServiceRef.current === serviceId) setError(errorMessage(caught));
    } finally {
      setServiceAction(serviceId, null);
    }
  };

  const selectService = (service: Project['services'][number]) => {
    setSetupOpen(false);
    if (service.id === selectedServiceId) {
      setError('');
      setNotice('');
      void refreshDetails(service.id).catch((caught) => setError(errorMessage(caught)));
      return;
    }
    selectServiceId(service.id);
    setDetails(null);
    setError('');
    setNotice('');
    setPortConflict(null);
    setPortConflictServiceId('');
    setConflictError('');
  };

  const resumeSetup = () => {
    setSetupOpen(true);
    selectServiceId('');
    setDetails(null);
    setError('');
    setNotice('');
    setPortConflict(null);
    setPortConflictServiceId('');
    setConflictError('');
  };

  const navigateHome = () => {
    setSetupOpen(false);
    selectServiceId('');
    setDetails(null);
    setError('');
    setNotice('');
    setPortConflict(null);
    setPortConflictServiceId('');
    setConflictError('');
  };

  return (
    <div className="app-shell">
      <aside className="sidebar">
        <button className="brand-lockup" onClick={navigateHome} aria-label="Go to project overview" title="Project overview">
          <span className="brand-mark"><span /></span>
          <span className="brand-type"><strong>nala<span>ctrl</span></strong><small>LOCAL PROCESS DESK</small></span>
        </button>

        <div className="project-nav-head">
          <span>PROJECTS</span>
          <span className="project-count">{workspace.projects.length} {workspace.projects.length === 1 ? 'project' : 'projects'}</span>
          <button className="add-project-button" onClick={chooseFolder} disabled={busyAction === 'folder'} title="Add project folder" aria-label="Add project folder"><Icon name="folder-plus" /></button>
        </div>
        <nav className="project-list" aria-label="Configured projects">
          {workspace.projects.map((project) => {
            return (
              <section key={project.id} className="project-group" aria-label={`${project.name} project`}>
                <div className="project-group-head">
                  <span className="project-group-icon"><Icon name="folder" /></span>
                  <span className="project-group-copy"><strong>{project.name}</strong><small title={project.folder}>{displayFolderPath(project.folder)}</small></span>
                </div>
                <div className="project-services" role="group" aria-label={`${project.name} services`}>
                  {project.services.map((service) => {
                    const active = service.id === selectedServiceId;
                    const status = serviceStatuses[service.id] ?? (active && details ? { running: details.running, pid: details.pid } : null);
                    const serviceAction = serviceActions[service.id];
                    const live = status?.running ?? false;
                    const stateLabel = serviceAction === 'run' ? 'STARTING' : serviceAction === 'stop' || serviceAction === 'terminate' ? 'STOPPING' : status ? (live ? 'RUNNING' : 'STOPPED') : 'CHECKING';
                    return (
                      <button key={service.id} className={`project-item${active ? ' selected' : ''}`} onClick={() => selectService(service)} aria-current={active ? 'page' : undefined} aria-label={`${service.name}, ${stateLabel.toLowerCase()}${service.port ? `, port ${service.port}` : ''}`} title={`${project.folder} · ${service.name}`}>
                        <span className={`project-dot${live ? ' live' : ''}`} aria-hidden="true" />
                        <span className="project-item-copy">
                          <strong>{service.name}</strong>
                          <small>{service.port ? `PORT ${service.port}` : 'NO PORT CHECK'} · {stateLabel}</small>
                          {live && status && status.pid > 0 && <small className="project-location">PID {status.pid}</small>}
                        </span>
                        {active && <Icon className="project-chevron" name="chevron" />}
                      </button>
                    );
                  })}
                  {!project.services.length && <div className="nav-empty">No services configured in this nala.yml.</div>}
                </div>
              </section>
            );
          })}
          {!workspace.projects.length && workspace.needsConfig && <div className="nav-empty">Finish setup to add this folder's project.</div>}
          {!workspace.projects.length && !workspace.folder && <div className="nav-empty">Add a project folder to get started.</div>}
        </nav>

        <div className="sidebar-bottom">
          <span className="local-indicator">LOCAL MACHINE</span>
          <span className="sidebar-footnote">Each nala.yml is one project. Its services can run independently.</span>
        </div>
      </aside>

      <main className="main-area">
        <header className="topbar">
          <div className="breadcrumb">
            {selected ? <button className="breadcrumb-home" onClick={navigateHome} aria-label="Go to project overview" title="Project overview">PROJECTS</button> : <span aria-current="page">PROJECTS</span>}
            <Icon name="chevron" /><strong title={setupOpen ? workspace.folder : selectedProject?.folder || workspace.folder}>{selectedProject?.name ?? (setupOpen && workspace.folder ? folderName(workspace.folder) : 'Overview')}</strong>
            {selected && <><Icon name="chevron" /><span className="crumb-project">{selected.name}</span></>}
          </div>
          <div className="topbar-actions">
            {(workspace.folder || workspace.projects.length > 0) && <span className="local-pill">This machine</span>}
            <button className="icon-button" onClick={chooseFolder} title="Add project folder" aria-label="Add project folder" disabled={busyAction === 'folder'}><Icon name="folder-plus" /></button>
          </div>
        </header>

        <div className="content-scroll">
          {error && <div className="message-banner error-banner" role="alert"><Icon name="warning" /><span>{error}</span><button onClick={() => setError('')} aria-label="Dismiss error"><Icon name="close" /></button></div>}
          {workspaceWarnings.length > 0 && <div className="message-banner error-banner" role="status" aria-live="polite"><Icon name="warning" /><span>Workspace warning: {workspaceWarnings.join(' ')}</span><button onClick={() => setWorkspaceWarnings([])} aria-label="Dismiss workspace warnings"><Icon name="close" /></button></div>}
          {notice && <div className="message-banner notice-banner" role="status"><Icon name="check" /><span>{notice}</span><button onClick={() => setNotice('')} aria-label="Dismiss notification"><Icon name="close" /></button></div>}
          {workspaceHydrating && <div className="message-banner notice-banner" role="status" aria-live="polite"><Icon name="refresh" /><span>Restoring saved projects…</span></div>}

          {!workspace.folder && !workspace.projects.length && <WelcomeScreen onChoose={chooseFolder} busy={busyAction === 'folder'} />}
          {workspace.folder && workspace.needsConfig && setupOpen && !selected && (
            <SetupScreen folder={workspace.folder} form={form} setForm={setForm} onSubmit={createConfig} busy={busyAction === 'config'} />
          )}
          {workspace.folder && workspace.needsConfig && selected && (
            <div className="pending-setup-banner" role="status">
              <span><strong>{folderName(workspace.folder)} needs setup</strong><small>Other projects and services keep running while you configure this folder.</small></span>
              <button className="secondary-button" onClick={resumeSetup}>Finish setup for {folderName(workspace.folder)}</button>
            </div>
          )}
          {workspace.folder && !workspace.needsConfig && workspace.projects.length === 0 && (
            <div className="empty-project-state">
              <div className="empty-glyph"><Icon name="server" /></div>
              <p className="eyebrow">NOTHING TO MANAGE</p>
              <h1>No project configured</h1>
              <p>Add a folder that contains <code>nala.yml</code> to monitor its configured project.</p>
              <button className="secondary-button" onClick={chooseFolder}><Icon name="folder-plus" /> Add project folder</button>
            </div>
          )}
          {workspace.projects.length > 0 && !serviceEntries.length && !workspace.needsConfig && (
            <div className="empty-project-state">
              <div className="empty-glyph"><Icon name="server" /></div>
              <p className="eyebrow">NO SERVICES</p>
              <h1>No services configured</h1>
              <p>Choose another project folder with configured services, or finish setup for the folder that needs it.</p>
              <button className="secondary-button" onClick={chooseFolder}><Icon name="folder-plus" /> Add project folder</button>
            </div>
          )}
          {workspace.needsConfig && !setupOpen && !selected && !serviceEntries.length && (
            <div className="empty-project-state">
              <div className="empty-glyph"><Icon name="folder-plus" /></div>
              <p className="eyebrow">SETUP PAUSED</p>
              <h1>Continue project setup</h1>
              <p><code>{folderName(workspace.folder)}</code> does not have a <code>nala.yml</code> yet.</p>
              <button className="secondary-button" onClick={resumeSetup}>Finish setup for {folderName(workspace.folder)}</button>
            </div>
          )}
          {serviceEntries.length > 0 && !selected && !setupOpen && (
            <ProjectOverview
              projects={workspace.projects}
              serviceStatuses={serviceStatuses}
              needsConfigFolder={workspace.needsConfig ? workspace.folder : ''}
              onSelectService={selectService}
              onResumeSetup={resumeSetup}
            />
          )}
          {selected && (
            <ProjectScreen
              project={selectedProject!}
              service={selected}
              details={details}
              busyAction={serviceActions[selected.id] ?? null}
              followLogs={followLogs}
              logScreenRef={logScreenRef}
              onRun={runService}
              onStop={stopService}
              onRefresh={() => void refreshDetails(selected.id).catch((caught) => setError(errorMessage(caught)))}
              onToggleFollow={() => setFollowLogs((value) => !value)}
            />
          )}
        </div>
      </main>

      {portConflict && selected && selected.id === portConflictServiceId && selectedProject && (
        <PortConflictDialog
          project={selectedProject}
          service={selected}
          owner={portConflict}
          error={conflictError}
          busy={serviceActions[selected.id] === 'terminate'}
          onCancel={() => { setPortConflict(null); setPortConflictServiceId(''); setConflictError(''); }}
          onConfirm={terminateAndRun}
        />
      )}
    </div>
  );
}

function ProjectOverview({
  projects, serviceStatuses, needsConfigFolder, onSelectService, onResumeSetup,
}: {
  projects: Project[];
  serviceStatuses: Record<string, ServiceStatus>;
  needsConfigFolder: string;
  onSelectService: (service: Project['services'][number]) => void;
  onResumeSetup: () => void;
}) {
  return (
    <section className="overview-screen" aria-labelledby="overview-title">
      <header className="overview-heading">
        <p className="eyebrow">LOCAL WORKSPACE</p>
        <h1 id="overview-title">Project overview</h1>
        <p>Select a service to inspect its process and output.</p>
      </header>
      {needsConfigFolder && (
        <div className="pending-setup-banner" role="status">
          <span><strong>{folderName(needsConfigFolder)} needs setup</strong><small>Its nala.yml has not been created. Other projects and services keep running.</small></span>
          <button className="secondary-button" onClick={onResumeSetup}>Finish setup for {folderName(needsConfigFolder)}</button>
        </div>
      )}
      <div className="overview-project-list">
        {projects.map((project) => (
          <section key={project.id} className="overview-project" aria-label={`${project.name} project`}>
            <header className="overview-project-head">
              <div><h2>{project.name}</h2><p title={project.folder}>{displayFolderPath(project.folder)}</p></div>
              <span>{project.services.length} {project.services.length === 1 ? 'service' : 'services'}</span>
            </header>
            {project.services.length > 0 ? (
              <div className="overview-service-list" role="group" aria-label={`${project.name} services`}>
                {project.services.map((service) => {
                  const status = serviceStatuses[service.id];
                  const stateLabel = status ? (status.running ? `Running · PID ${status.pid}` : 'Stopped') : 'Select to check status';
                  return (
                    <button key={service.id} className="overview-service" onClick={() => onSelectService(service)} aria-label={`${project.name}, ${service.name}, ${stateLabel}`}>
                      <span className="overview-service-copy"><strong>{service.name}</strong><small>{service.port ? `PORT ${service.port}` : 'NO PORT CHECK'}</small></span>
                      <span className={`overview-service-status${status?.running ? ' live' : ''}`}><i aria-hidden="true" />{stateLabel}</span>
                      <Icon name="chevron" />
                    </button>
                  );
                })}
              </div>
            ) : <p className="overview-no-services">No services configured in this nala.yml.</p>}
          </section>
        ))}
      </div>
    </section>
  );
}

function WelcomeScreen({ onChoose, busy }: { onChoose: () => void; busy: boolean }) {
  return (
    <section className="welcome-screen">
      <div className="welcome-content">
        <div className="welcome-emblem"><Icon name="terminal" /></div>
        <p className="eyebrow">A CLEAR VIEW OF WHAT'S RUNNING</p>
        <h1>Your project,<br /><em>under control.</em></h1>
        <p className="welcome-copy">Choose a project folder to monitor each configured service, read its output, and start or stop it from one quiet place.</p>
        <button className="primary-button welcome-button" onClick={onChoose} disabled={busy}>
          <Icon name="folder-plus" /> {busy ? 'Opening folder…' : 'Choose project folder'}
        </button>
        <div className="welcome-note"><span className="note-line" /> Looks for <code>nala.yml</code> at the folder root</div>
      </div>
      <div className="welcome-footer"><span>LOCAL PROCESS MONITOR</span><span>ONE PROJECT PER CONFIG</span></div>
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
        <p>There’s no <code>nala.yml</code> here yet. Configure how to run this folder as a project named <strong>{folderName(folder)}</strong>.</p>
        <div className="setup-location"><Icon name="folder" /><span>{folder}</span></div>
      </div>

      <form className="setup-card" onSubmit={onSubmit}>
        <div className="setup-card-head"><div><span className="eyebrow">NEW CONFIGURATION</span><h2>Project setup</h2></div><span className="config-file-badge">nala.yml</span></div>
        <label className="field-label" htmlFor="project-cwd">Working directory</label>
        <input id="project-cwd" className="text-field mono-field" value={form.cwd} onChange={(event) => update('cwd', event.target.value)} placeholder="." required autoComplete="off" />
        <p className="field-help">Relative to the folder you chose, or an absolute path.</p>
        <label className="field-label" htmlFor="project-run">Run command</label>
        <input id="project-run" className="text-field mono-field" value={form.run} onChange={(event) => update('run', event.target.value)} placeholder="go run ." required autoComplete="off" />
        <label className="field-label" htmlFor="project-port">Port <span>OPTIONAL</span></label>
        <input id="project-port" className="text-field mono-field" value={form.port} onChange={(event) => update('port', event.target.value)} placeholder="Auto-detect from command" type="number" min="1" max="65535" />
        <p className="field-help">A clear port flag or common dev-server default is detected when this is blank.</p>
        <div className="config-preview"><span>CREATES</span><code>{folderName(folder)} · nala.yml · cwd · run · port</code></div>
        <button className="primary-button setup-submit" type="submit" disabled={busy}>{busy ? 'Creating config…' : 'Create nala.yml'}</button>
      </form>
    </section>
  );
}

function ProjectScreen({
  project, service, details, busyAction, followLogs, logScreenRef, onRun, onStop, onRefresh, onToggleFollow,
}: {
  project: Project;
  service: Project['services'][number];
  details: ProjectDetails | null;
  busyAction: ServiceAction | null;
  followLogs: boolean;
  logScreenRef: RefObject<HTMLDivElement | null>;
  onRun: () => void;
  onStop: () => void;
  onRefresh: () => void;
  onToggleFollow: () => void;
}) {
  const running = details?.running ?? false;
  const owner = details?.portOwner ?? null;
  const lines = details?.logLines ?? [];
  const isBusy = busyAction !== null || details === null;
  const processState = !details ? 'checking' : busyAction === 'run' ? 'starting' : busyAction === 'stop' || busyAction === 'terminate' ? 'stopping' : running ? 'running' : 'stopped';
  const processStateLabel = processState === 'checking' ? 'Checking process' : processState === 'starting' ? 'Starting process' : processState === 'stopping' ? 'Stopping process' : running ? 'Running' : 'Stopped';
  return (
    <section className="project-screen">
      <div className="project-heading">
        <div className="project-heading-copy">
          <p className="eyebrow">PROJECT / {project.name}</p>
          <div className="project-title-row">
            <span className={`large-status-dot${running ? ' live' : details ? ' stopped' : ' checking'}`} aria-hidden="true" />
            <div className="project-title-copy">
              <h1>{service.name}</h1>
              <div className={`process-status${running ? ' live' : ''}`} role="status" aria-live="polite">
                <strong>{processStateLabel}</strong>
                <span>{running ? `PID ${details?.pid}` : processState === 'checking' ? 'Reading process state' : owner ? `${owner.name} holds the port` : 'No managed process'}</span>
              </div>
            </div>
          </div>
          <p className="project-folder-path" title={project.folder}>{displayFolderPath(project.folder)}</p>
          <p className="run-command"><Icon name="terminal" /><span>Run command</span><code title={service.run}>{service.run}</code></p>
        </div>
        <div className="project-actions">
          <button className="icon-button refresh-button" onClick={onRefresh} title="Refresh service" aria-label="Refresh service"><Icon name="refresh" /></button>
          {!details ? (
            <button className="secondary-button" disabled><Icon name="refresh" />Checking process…</button>
          ) : running ? (
            <button className="stop-button" onClick={onStop} disabled={isBusy}><Icon name="stop" />{busyAction === 'stop' || busyAction === 'terminate' ? 'Stopping…' : 'Stop service'}</button>
          ) : (
            <button className="run-button" onClick={onRun} disabled={isBusy}><Icon name="play" />{busyAction === 'run' ? 'Starting…' : 'Run service'}</button>
          )}
        </div>
      </div>

      <div className="project-meta" aria-label="Project details">
        <div className="project-meta-item">
          <span>PORT</span>
          <strong>{service.port ? `:${service.port}` : 'Not configured'}</strong>
          <small>{owner ? `${owner.name} · PID ${owner.pid}` : service.port ? 'Available' : 'Port check is not configured'}</small>
        </div>
        <div className="project-meta-item">
          <span>WORKING DIRECTORY</span>
          <code title={service.cwd}>{service.cwd}</code>
        </div>
      </div>

      <section className="log-panel" aria-label={`${project.name} ${service.name} log stream`}>
        <div className="log-panel-head">
          <div className="log-title"><Icon className="log-terminal-icon" name="terminal" /><div><h2>Output stream</h2><p>{details?.logPath ?? service.logPath}</p></div></div>
          <div className="log-controls">
            <span className="log-count">{details ? `${lines.length} lines · ${formatBytes(details.logSize)}` : 'Loading log…'}</span>
            <button className={`follow-button${followLogs ? ' following' : ''}`} onClick={onToggleFollow} aria-pressed={followLogs}><span className="follow-dot" />{followLogs ? 'Following' : 'Follow logs'}</button>
          </div>
        </div>
        <div ref={logScreenRef} className="log-screen" role="log" aria-live="off" aria-relevant="additions text">
          {lines.length ? lines.map((line, index) => (
            <div className="log-line" key={`${index}-${line}`}><span className="line-number">{String(index + 1).padStart(3, '0')}</span><span className="line-text">{line || ' '}</span></div>
          )) : (
            <div className="log-empty"><div className="log-empty-mark"><Icon name="terminal" /></div><strong>No output yet</strong><span>{running ? 'Waiting for the service to write its first line.' : 'Run this service to start capturing its output.'}</span></div>
          )}
        </div>
        <div className="log-panel-foot"><span><i className={running ? 'foot-live' : ''} />{running ? 'STREAM ACTIVE' : 'STREAM IDLE'}</span><span>Live output · latest 250 lines</span></div>
      </section>
    </section>
  );
}

function PortConflictDialog({
  project, service, owner, error, busy, onCancel, onConfirm,
}: {
  project: Project;
  service: Project['services'][number];
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
        <h2 id="conflict-title">Port {service.port ?? 'configured for this service'} is already in use.</h2>
        <p className="dialog-copy">Nala Ctrl found a process listening on this port. Stop this process and run <strong>{service.name}</strong> in <strong>{project.name}</strong>?</p>
        <div className="process-identity">
          <div className="process-avatar"><Icon name="terminal" /></div>
          <div className="process-copy"><strong>{owner.name}</strong><span>{owner.managedProject || owner.managedService ? `Managed ${owner.managedProject || 'project'}${owner.managedService ? ` · ${owner.managedService}` : ''}` : 'Existing process on this machine'}</span></div>
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
