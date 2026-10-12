export namespace main {
	
	export class CreateProjectInput {
	    cwd: string;
	    run: string;
	    port: string;
	
	    static createFrom(source: any = {}) {
	        return new CreateProjectInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.cwd = source["cwd"];
	        this.run = source["run"];
	        this.port = source["port"];
	    }
	}
	export class ProcessIdentity {
	    pid: number;
	    name: string;
	    createTime: number;
	    canTerminate: boolean;
	    managedProject: string;
	    terminationNote: string;
	
	    static createFrom(source: any = {}) {
	        return new ProcessIdentity(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.pid = source["pid"];
	        this.name = source["name"];
	        this.createTime = source["createTime"];
	        this.canTerminate = source["canTerminate"];
	        this.managedProject = source["managedProject"];
	        this.terminationNote = source["terminationNote"];
	    }
	}
	export class Project {
	    name: string;
	    cwd: string;
	    run: string;
	    port?: number;
	    logPath: string;
	
	    static createFrom(source: any = {}) {
	        return new Project(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.cwd = source["cwd"];
	        this.run = source["run"];
	        this.port = source["port"];
	        this.logPath = source["logPath"];
	    }
	}
	export class ProjectDetails {
	    name: string;
	    cwd: string;
	    run: string;
	    port?: number;
	    logPath: string;
	    running: boolean;
	    pid: number;
	    portOwner?: ProcessIdentity;
	    logLines: string[];
	    logSize: number;
	
	    static createFrom(source: any = {}) {
	        return new ProjectDetails(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.cwd = source["cwd"];
	        this.run = source["run"];
	        this.port = source["port"];
	        this.logPath = source["logPath"];
	        this.running = source["running"];
	        this.pid = source["pid"];
	        this.portOwner = this.convertValues(source["portOwner"], ProcessIdentity);
	        this.logLines = source["logLines"];
	        this.logSize = source["logSize"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RunResult {
	    started: boolean;
	    pid: number;
	    portOwner?: ProcessIdentity;
	
	    static createFrom(source: any = {}) {
	        return new RunResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.started = source["started"];
	        this.pid = source["pid"];
	        this.portOwner = this.convertValues(source["portOwner"], ProcessIdentity);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class WorkspaceState {
	    folder: string;
	    needsConfig: boolean;
	    projects: Project[];
	
	    static createFrom(source: any = {}) {
	        return new WorkspaceState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.folder = source["folder"];
	        this.needsConfig = source["needsConfig"];
	        this.projects = this.convertValues(source["projects"], Project);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

