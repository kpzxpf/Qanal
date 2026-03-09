export namespace transfer {
	
	export class FileInfo {
	    name: string;
	    size: number;
	    path: string;
	
	    static createFrom(source: any = {}) {
	        return new FileInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.size = source["size"];
	        this.path = source["path"];
	    }
	}
	export class PeerInfo {
	    lan: string;
	    wan: string;
	    code: string;
	    key: string;
	    relay: string;
	
	    static createFrom(source: any = {}) {
	        return new PeerInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.lan = source["lan"];
	        this.wan = source["wan"];
	        this.code = source["code"];
	        this.key = source["key"];
	        this.relay = source["relay"];
	    }
	}
	export class SendResult {
	    code: string;
	    key: string;
	    fileHash?: string;
	
	    static createFrom(source: any = {}) {
	        return new SendResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.code = source["code"];
	        this.key = source["key"];
	        this.fileHash = source["fileHash"];
	    }
	}

}

