export namespace main {
	
	export class CallLog {
	    id: string;
	    number: string;
	    direction: string;
	    status: string;
	    // Go type: time
	    timestamp: any;
	    durationSec: number;
	
	    static createFrom(source: any = {}) {
	        return new CallLog(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.number = source["number"];
	        this.direction = source["direction"];
	        this.status = source["status"];
	        this.timestamp = this.convertValues(source["timestamp"], null);
	        this.durationSec = source["durationSec"];
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
	export class Contact {
	    id: string;
	    name: string;
	    sipAddress: string;
	
	    static createFrom(source: any = {}) {
	        return new Contact(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.sipAddress = source["sipAddress"];
	    }
	}

}

