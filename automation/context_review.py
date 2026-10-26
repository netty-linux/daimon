"""Context and grouping over verified source data; decisions never suppress scanners."""
import ast
import hashlib
import json
from collections import Counter, deque

CLASSES = {'confirmed-vulnerability', 'contextual-risk-mitigated', 'demonstrated-false-positive', 'inconclusive'}
INTEGRATION_FILES = ('python/harness2_bridge/worker.py', 'python/harness2_native_substrate/substrate.py',
                     'internal/harness2bridge/bridge.go', 'internal/harness2bridge/process_linux.go',
                     'internal/harness2bridge/process_windows.go', 'internal/harness2substrate/substrate.go',
                     'internal/harness2substrate/gates.go')
REQUIRED_TESTS = ['test_context_in_memory_sensitive_sinks_not_called', 'test_context_disallowed_components_stop_before_upstream',
                  'test_context_compile_does_not_execute', 'test_context_original_runmark_collision']


def digest(data):
    return hashlib.sha256(data).hexdigest()


def functions(path, source):
    """Lexical index, not a complete Python dispatch/taint analysis."""
    tree = ast.parse(source, filename=path)
    result = {'<module>': tree}
    def walk(node, prefix):
        for child in ast.iter_child_nodes(node):
            if isinstance(child, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
                name = prefix + child.name
                result[name] = child
                walk(child, name + '.')
            else:
                walk(child, prefix)
    walk(tree, '')
    return result


def enclosing(index, line):
    candidates = [(n.end_lineno - n.lineno, name) for name, n in index.items() if name != '<module>' and n.lineno <= line <= n.end_lineno]
    return min(candidates)[1] if candidates else '<module>'


def name_of(node):
    if isinstance(node, ast.Name): return node.id
    if isinstance(node, ast.Attribute): return name_of(node.value) + '.' + node.attr
    return '<dynamic>'


def records(raw, sources):
    indexes = {p:functions(p, b) for p,b in sources.items() if p.endswith('.py')}
    rows=[]
    for row in raw['bandit']['results']:
        rows.append(dict(tool='bandit',path=row['filename'],line=row['line_number'],column=row.get('col_offset'),
                         rule=row['test_id'],severity=row['issue_severity'].lower(),description=row['issue_text']))
    for row in raw['analysis']['risk_signals']:
        rows.append(dict(tool='ast',**{k:row[k] for k in ('path','line','column','rule','severity')},description=row['call']))
    seen=set()
    for row in rows:
        path=row['path']; source=sources[path]; lines=source.splitlines()
        if not 1 <= row['line'] <= len(lines): raise ValueError('Finding outside source')
        row['source_sha256']=digest(source)
        row['line_sha256']=digest(lines[row['line']-1])
        row['function']=enclosing(indexes[path],row['line'])
        node=indexes[path][row['function']]
        row['function_sha256']=digest(ast.dump(node,include_attributes=False).encode())
        identity={k:row[k] for k in ('tool','path','line','column','rule','source_sha256','line_sha256')}
        row['id']=digest(json.dumps(identity,sort_keys=True).encode())
        if row['id'] in seen: raise ValueError('Duplicate original finding identity')
        seen.add(row['id'])
        row['group']=row['path']+':'+row['function']
    return sorted(rows,key=lambda r:r['id'])


def approximate_graph(sources, integration):
    nodes,edges={},[]
    for path,source in sorted({**sources,**integration}.items()):
        if not path.endswith('.py'):continue
        index=functions(path,source)
        for fn,node in index.items():
            key=path+':'+fn
            nodes[key]=dict(path=path,function=fn,line=getattr(node,'lineno',1),source_sha256=digest(source),
                            function_sha256=digest(ast.dump(node,include_attributes=False).encode()))
        for node in ast.walk(index['<module>']):
            if not isinstance(node,ast.Call):continue
            owner=enclosing(index,node.lineno);call=name_of(node.func)
            # Scope-aware lexical resolution of plain local calls only.
            candidates=[]
            if isinstance(node.func,ast.Name):
                prefix=owner.rsplit('.',1)[0]+'.' if '.' in owner else ''
                candidates=[prefix+call,call]
            target=next((path+':'+x for x in candidates if x in index and not isinstance(index[x],ast.ClassDef)),None)
            edges.append(dict(source=path+':'+owner,target=target,call=call,line=node.lineno,
                              resolution='lexical-overapproximation' if target else 'unresolved-dispatch-or-external',
                              guard='branch predicates not solved'))
    worker='python/harness2_bridge/worker.py:';native='python/harness2_native_substrate/substrate.py:';tree='harness2/core/tree.py:'
    reviewed=[(worker+'validate_manifest',tree+'validate','fixed validated in-memory call'),
              (native+'NativeSubstrate.preview',tree+'validate','fixed adapter self._validate binding'),
              (native+'load',worker+'load_upstream','trusted loader calls existing verified bridge')]
    for origin in (worker+'load_upstream',native+'load'):
        loaded=['harness2/__init__.py','harness2/core/__init__.py','harness2/core/tree.py'] if origin.startswith(worker) else ['harness2/substrate/base.py']
        reviewed.extend((origin,path+':<module>','verified snapshot import, not user code') for path in loaded)
    for origin,target,basis in reviewed:
        if origin not in nodes or target not in nodes: raise ValueError('Reviewed boundary no longer exists')
        edges.append(dict(source=origin,target=target,call=basis,line=nodes[origin]['line'],resolution='reviewed-integration-edge',guard='fixed loader/input contracts; see regression tests'))
    roots=[worker+'main',native+'load',native+'NativeSubstrate.preview']
    adjacent={n:[] for n in nodes}
    for edge in edges:
        if edge['target']:adjacent[edge['source']].append(edge['target'])
    paths={r:[r] for r in roots};todo=deque(roots)
    while todo:
        origin=todo.popleft()
        for target in adjacent[origin]:
            if target not in paths:paths[target]=paths[origin]+[target];todo.append(target)
    return dict(nodes=nodes,edges=edges,roots=roots,paths=paths,
                limitations=['Approximate lexical graph: aliasing, callbacks, class dispatch, reflection and branch predicates are not fully resolved.',
                             'No resolved path is not proof of unreachability; loaded module and callable function are different scopes.',
                             'Go/process/auth boundaries are reviewed source references, not inferred Python graph edges.'])


def framed_reference(parts):
    """Proposed framing only; not an upstream change or production implementation."""
    return b''.join(len(name.encode()).to_bytes(8,'big')+name.encode()+len(data).to_bytes(8,'big')+data for name,data in parts)


def legacy_stream(parts):
    return b''.join(name.encode()+b'\0'+data+b'\0' for name,data in parts)


def verify_claim(row,decision,sources):
    claim=decision['claim'];category=decision['classification']
    if category not in CLASSES or not decision.get('reason') or not decision.get('evidence'):
        raise ValueError('Unjustified or unknown review disposition')
    node=functions(row['path'],sources[row['path']])[row['function']]
    text=ast.unparse(node)
    if claim=='pending-flow-review':
        if category!='inconclusive':raise ValueError('Pending review cannot resolve a finding')
    elif claim=='public-deterministic-shuffle':
        if (row['path'],row['function'],row['rule']) not in [('harness2/core/selection.py','assign_ids','B324'),('harness2/core/selection.py','assign_ids','B311')]:
            raise ValueError('Shuffle claim outside reviewed findings')
        if category!='demonstrated-false-positive' or not all(s in text for s in ('hashlib.sha1','random.Random(seed).shuffle(ordered)','enumerate(ordered)')):
            raise ValueError('Shuffle evidence changed')
    elif claim=='unframed-run-reuse':
        if (row['path'],row['function'],row['rule'],category)!=('harness2/core/runmark.py','fingerprint','B324','confirmed-vulnerability'):
            raise ValueError('Fingerprint proof outside exact reviewed finding')
        if not all(s in text for s in ('hashlib.sha1()', 'p.read_bytes()', 'h.hexdigest()[:16]')) or text.count("h.update(b'\\x00')")!=2:
            raise ValueError('Fingerprint serialization differs from reviewed source')
        a=[('a',b'x\0b\0y')];b=[('a',b'x'),('b',b'y')]
        if legacy_stream(a)!=legacy_stream(b) or framed_reference(a)==framed_reference(b):raise ValueError('Collision proof invalid')
    elif claim=='truncated-shared-job-name':
        if (row['path'],row['function'],row['rule'],category)!=('harness2/benchmarks/workbuddy.py','run_batch','B324','inconclusive'):
            raise ValueError('Job namespace claim outside reviewed finding')
        if not all(s in text for s in ('.hexdigest()[:8]', 'yaml_path.write_text', 'yaml_path.unlink', '_label_lock(label)')):
            raise ValueError('Shared job namespace changed')
    elif claim=='fixed-validator-contract':
        if row['path']!='harness2/core/tree.py' or row['tool']!='ast' or category!='contextual-risk-mitigated':
            raise ValueError('Validation contract not a general exemption')
        if row['function'] not in ('read_tree','write_tree','check_hooks','_py_compiles'):
            raise ValueError('Unknown sensitive runtime flow')
    elif claim=='noncredential-scalar':
        if row['rule']!='B105' or category!='demonstrated-false-positive':raise ValueError('Scalar claim is not a generic password waiver')
        line=sources[row['path']].decode().splitlines()[row['line']-1].strip()
        if (row['path'],line) not in [('harness2/benchmarks/jobbench.py','"all_pass": False,'),
                                     ('harness2/benchmarks/jobbench.py','token = ""'),
                                     ('third_party/lab_runner/opencode/run_opencode.py','OUTPUT_TOKEN_MAX = "64000"')]:
            raise ValueError('Credential scalar evidence changed')
    else:raise ValueError('Unsupported contextual claim')


def review(raw,sources,integration,ledger):
    current=records(raw,sources)
    if ledger['upstream_revision']!=raw['snapshot']['revision'] or ledger['baseline_count']!=len(current):
        raise ValueError('Review baseline revision/count mismatch')
    actual_hashes={p:digest(b.replace(b'\r\n',b'\n')) for p,b in integration.items()}
    if actual_hashes!=ledger['integration_hashes']:raise ValueError('Integration changed after contextual review')
    if set(ledger['decisions'])!={r['id'] for r in current}:raise ValueError('Missing, new or unreviewed finding identity')
    graph=approximate_graph(sources,integration)
    groups={};counts=Counter();entries=[]
    for row in current:
        decision=ledger['decisions'][row['id']]
        verify_claim(row,decision,sources)
        key=row['path']+':'+row['function']
        result=dict(**row,**decision,approximate_path=graph['paths'].get(key),
                    reachability='resolved-path-branches-unproven' if key in graph['paths'] else 'no-resolved-path-not-proof-of-unreachability')
        entries.append(result);counts[decision['classification']]+=1
        group=groups.setdefault(key,dict(ids=[],effects=set(),classifications=set()))
        group['ids'].append(row['id']);group['effects'].add(row['rule']);group['classifications'].add(decision['classification'])
    groups={k:dict(ids=v['ids'],effects=sorted(v['effects']),classifications=sorted(v['classifications'])) for k,v in sorted(groups.items())}
    highs=[e for e in entries if e['tool']=='bandit' and e['severity']=='high']
    if len(highs)!=3:raise ValueError('HIGH set changed; explicit new review required')
    return dict(integrity='PASS',entries=entries,groups=groups,counts=dict(counts),high_reviews=highs,graph=graph,
                original_findings_retained=len(entries),proof_tests_required=REQUIRED_TESTS,
                security_gate_promoted=False,coverage='UNVERIFIED',limitations=graph['limitations'])
