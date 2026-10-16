#!/usr/bin/env python3
"""Summarize Finicky's local timing metadata. Never read the ordinary request logs."""
import argparse
import collections
import datetime
import json
import math
import re
from pathlib import Path
import statistics


def parse_time(value):
    # Go emits variable fractional precision; older Python needs six digits.
    value = re.sub(r'\.(\d+)', lambda m: '.' + (m[1] + '000000')[:6], value)
    return datetime.datetime.fromisoformat(value.replace('Z', '+00:00'))


def summarize(directory, slow_ms=1000, limit=20):
    events = []
    malformed = 0
    for path in directory.glob('events.jsonl*'):
        with path.open() as stream:
            for line in stream:
                try:
                    events.append(json.loads(line))
                except (ValueError, TypeError):
                    malformed += 1
    events.sort(key=lambda e: parse_time(e['time']))
    requests = collections.defaultdict(dict)
    stages = collections.defaultdict(list)
    foreground = []
    dropped = 0
    for event in events:
        dropped += event.get('dropped_events', 0)
        if event.get('request_id'):
            requests[(event['session'], event['request_id'])][event['event']] = event
        if event['event'] == 'foreground_observed':
            foreground.append(event)
        if 'duration_ms' in event:
            stages[event['event']].append(event['duration_ms'])
    completed = []
    incomplete = []
    aliases = {'Google Chrome': 'com.google.Chrome', 'Firefox': 'org.mozilla.firefox',
               'Safari': 'com.apple.Safari', 'Spotify': 'com.spotify.client'}
    parse = parse_time
    for key, request in requests.items():
        receipt = request.get('received', {})
        dispatch = request.get('dispatch_started', {})
        end = request.get('dispatch_completed', {})
        row = dict(session=key[0], request_id=key[1], time=receipt.get('time'),
                   hostname=dispatch.get('hostname', receipt.get('hostname', '')),
                   opener_app=receipt.get('opener_app', ''),
                   app=dispatch.get('app', ''), profile=dispatch.get('profile', ''),
                   previous_receipt_ms=receipt.get('previous_receipt_ms'),
                   total_ms=end.get('since_receipt_ms'),
                   background=dispatch.get('background', False), dry_run=dispatch.get('dry_run', False),
                   stages_ms={kind: e['duration_ms'] for kind, e in request.items() if 'duration_ms' in e},
                   errors=[e['error_class'] for e in request.values() if 'error_class' in e])
        row['handoff_outcome'] = next((status for status in ('acknowledged', 'not_sent', 'rejected', 'unknown')
                                       if 'chrome_handoff_' + status in request), None)
        row['handoff_fallback_reason'] = next((kind.removeprefix('chrome_handoff_fallback_') for kind in request
                                                if kind.startswith('chrome_handoff_fallback_')), None)
        if not receipt or not end:
            incomplete.append(row)
            continue
        app = aliases.get(row['app'], row['app'])
        row['foreground_at_receipt'] = receipt.get('foreground_app') == app
        candidates = [e for e in foreground if e.get('session') == key[0] and e.get('app') == app
                      and 0 <= (parse(e['time']) - parse(receipt['time'])).total_seconds() <= 15]
        row['first_matching_foreground_ms'] = (
            (parse(candidates[0]['time']) - parse(receipt['time'])).total_seconds() * 1000 if candidates else None)
        completed.append(row)
    def stats(values):
        values = sorted(values)
        return dict(count=len(values), median_ms=statistics.median(values),
                    p95_ms=values[max(0, math.ceil(.95*len(values))-1)], max_ms=max(values)) if values else {}
    groups = collections.defaultdict(list)
    idle_groups = collections.defaultdict(list)
    for row in completed:
        if row['dry_run']:
            continue
        groups[f"{row['app']} / {row['profile'] or '(default)'}"].append(row['total_ms'])
        idle = row['previous_receipt_ms']
        bucket = 'first in process' if idle is None else ('under 1 minute' if idle < 60000 else '1-10 minutes' if idle < 600000 else 'over 10 minutes')
        idle_groups[bucket].append(row['total_ms'])
    live = [row for row in completed if not row['dry_run']]
    handoffs = [row for row in live if row['handoff_outcome']]
    return dict(completed=len(live), incomplete_count=len(incomplete), dropped_events=dropped, malformed_lines=malformed,
                chrome_handoff=dict(outcomes=dict(collections.Counter(row['handoff_outcome'] for row in handoffs)),
                                    fallback_reasons=dict(collections.Counter(row['handoff_fallback_reason'] for row in handoffs
                                                                             if row['handoff_fallback_reason'])),
                                    acknowledged_duration=stats([row['stages_ms']['chrome_handoff'] for row in handoffs
                                        if row['handoff_outcome'] == 'acknowledged' and 'chrome_handoff' in row['stages_ms']])),
                total=stats([row['total_ms'] for row in live]),
                stages={key: stats(values) for key, values in stages.items()},
                destinations={key: stats(values) for key, values in groups.items()},
                idle_intervals={key: stats(values) for key, values in idle_groups.items()},
                slow_events=sorted([row for row in live if row['total_ms'] >= slow_ms], key=lambda r: r['total_ms'], reverse=True)[:limit],
                recent_events=live[-limit:], incomplete_events=incomplete[-limit:],
                limitations='Timings start at the Go URL handler. Chrome acknowledgement means command acceptance, not page readiness. Foreground events are correlations, not proof of the correct tab or page readiness. Overlapping requests can share a foreground event. Missing events can mean queue overflow, disk failure, rotation, or abrupt exit. The idle interval resets on restart.')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--dir', type=Path, default=Path.home() / 'Library/Logs/Finicky/timing')
    parser.add_argument('--slow-ms', type=float, default=1000)
    parser.add_argument('--limit', type=int, default=20)
    parser.add_argument('--json', action='store_true')
    args = parser.parse_args()
    report = summarize(args.dir, args.slow_ms, args.limit)
    if args.json:
        print(json.dumps(report, indent=2))
        return
    print(f"Completed: {report['completed']}; incomplete: {report['incomplete_count']}; dropped: {report['dropped_events']}; malformed: {report['malformed_lines']}")
    print(f"Chrome handoff outcomes: {report['chrome_handoff']['outcomes']}")
    print(f"Chrome handoff fallback reasons: {report['chrome_handoff']['fallback_reasons']}")
    for section in ['total', 'stages', 'destinations', 'idle_intervals']:
        print(f'\n{section}:')
        groups = {'receipt to dispatch completion': report[section]} if section == 'total' else report[section]
        for label, values in groups.items():
            if values:
                print(f"  {label}: n={values['count']} median={values['median_ms']:.1f}ms p95={values['p95_ms']:.1f}ms max={values['max_ms']:.1f}ms")
    print(f'\nSlow events (at least {args.slow_ms:g}ms):')
    for row in report['slow_events']:
        print(f"  {row['time']} {row['hostname']} -> {row['app']} / {row['profile'] or '(default)'}: {row['total_ms']:.1f}ms")
        print(f"    stages={row['stages_ms']}; errors={row['errors']}; foreground correlation={row['first_matching_foreground_ms']}ms")
    if report['incomplete_count']:
        print('\nSome requests lack receipt or completion events. Inspect --json for details.')
    print('\n' + report['limitations'])


if __name__ == '__main__':
    main()
