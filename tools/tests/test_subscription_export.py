"""Frozen route catalog, ciphertext and policy projection used by Go import tests."""
from copy import deepcopy
import json
import importlib.util
import os
from pathlib import Path
import sqlite3
import tempfile
import unittest
from unittest.mock import patch

from test_portal_export import fixture, ROOT, PEPPER, STAMP
import marzban_portal_export as portal
import marzban_subscription_export as subscriptions

CIPHERS = json.loads((ROOT / 'internal/web/service/testdata/marzban_stage4_synthetic_ciphertexts.json').read_text())
POLICY = {'proxy-groups': [{'name': '🚀 节点选择', 'type': 'select', 'proxies': [subscriptions.LOCAL, 'MGMA · External']},
                           {'name': 'AI', 'type': 'select', 'proxies': ['🚀 节点选择']}], 'rules': ['MATCH,🚀 节点选择'], 'x-mgma-secure-dns': True}


def stage4_fixture(db):
    core, mapping, inventory, _, _ = fixture(db)
    core['inbounds'][0]['streamSettings']['realitySettings'] = {'publicKey': 'A' * 43, 'serverNames': ['vision.example.test'], 'shortIds': ['abcd']}
    core['inbounds'][1].update(port=8443, streamSettings={'network': 'xhttp', 'security': 'none', 'xhttpSettings': {'path': '/images/synthetic/'}})
    core['inbounds'][2].update(port=8444)
    profile = {'label': 'IPv6 download', 'enabled': True, 'address': 'up.example.test', 'port': 443,
               'streamSettings': {'network': 'xhttp', 'security': 'tls', 'tlsSettings': {'serverName': 'up.example.test', 'alpn': ['h2']},
                                  'xhttpSettings': {'path': '/images/synthetic/', 'mode': 'packet-up', 'extra': {'xmux': {'maxConcurrency': '16-32'},
                                                       'downloadSettings': {'address': '2001:db8::8', 'port': 443, 'network': 'xhttp', 'security': 'tls',
                                                                            'tlsSettings': {'serverName': 'cdn.example.test', 'alpn': ['h2']},
                                                                            'xhttpSettings': {'path': '/images/synthetic/'}}}}}}
    core['inbounds'][1]['x-marzban-xhttp-profiles'] = [profile, {'label': 'Disabled profile', 'enabled': False}]
    db.execute('CREATE TABLE hosts (id INTEGER PRIMARY KEY, inbound_tag TEXT, remark TEXT, address TEXT, port INTEGER, path TEXT, sni TEXT, host TEXT, security TEXT, alpn TEXT, fingerprint TEXT, allowinsecure INTEGER, is_disabled INTEGER, mux_enable INTEGER, fragment_setting TEXT, noise_setting TEXT, random_user_agent INTEGER, use_sni_as_host INTEGER, cascade_mode TEXT, cascade_entry_id INTEGER, cascade_entry_ciphertext TEXT)')
    for index, tag in enumerate(mapping, 1):
        db.execute('INSERT INTO hosts VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)', (index, tag, tag, f'host-{index}.example.test', None, None, None, None, 'inbound_default', 'none', 'none', 0, 0, 0, None, None, 0, 0, 'replace' if index == 1 else 'off', 31 if index == 1 else None, None))
    db.execute('CREATE TABLE external_subscription_sources(id INTEGER PRIMARY KEY, owner_account_id INTEGER, name TEXT, group_name TEXT, url_ciphertext TEXT, enabled INTEGER, refresh_interval_minutes INTEGER, status TEXT, node_count INTEGER, skipped_node_count INTEGER, created_at TEXT, updated_at TEXT)')
    db.execute('CREATE TABLE external_subscription_nodes(id INTEGER PRIMARY KEY, source_id INTEGER, external_key TEXT, name TEXT, proxy_type TEXT, payload_ciphertext TEXT, position INTEGER, is_active INTEGER)')
    db.execute('CREATE TABLE plan_external_selected_nodes(selection_id INTEGER, node_id INTEGER)')
    db.execute('INSERT INTO external_subscription_sources VALUES (12,NULL,?,?,?,?,?,?,?,?,?,?)', ('Shared', 'Airport', CIPHERS['url'], 1, 360, 'ready', 2, 0, STAMP, STAMP))
    db.execute('INSERT INTO external_subscription_sources VALUES (13,5,?,?,?,?,?,?,?,?,?,?)', ('Private', 'Airport', '', 1, 360, 'ready', 1, 0, STAMP, STAMP))
    for ident, source, label, pos in ((31, 12, 'shared', 0), (32, 12, 'unselected', 1), (33, 13, 'private', 0)):
        node = CIPHERS['nodes'][label]
        db.execute('INSERT INTO external_subscription_nodes VALUES (?,?,?,?,?,?,?,1)', (ident, source, node['key'], node['payload']['name'], node['payload']['type'], node['ciphertext'], pos))
    db.execute("INSERT INTO plan_external_source_selections VALUES (2,4,12,'selected')")
    db.execute('INSERT INTO plan_external_selected_nodes VALUES (2,31)')
    db.execute('UPDATE subscription_plans SET group_members=? WHERE id=4', (json.dumps({'AI': ['inbound:TEST_XHTTP', 'external']}),))
    db.commit()
    routes, hosts = subscriptions.catalog(db, core, mapping)
    return core, mapping, inventory, routes, hosts


class SubscriptionExportTests(unittest.TestCase):
    def setUp(self):
        self.db = sqlite3.connect(':memory:'); self.addCleanup(self.db.close)
        self.inputs = stage4_fixture(self.db)
        self.env = patch.dict(os.environ, {'MGMA_TOKEN_PEPPER': PEPPER, 'EXTERNAL_SUBSCRIPTION_ENCRYPTION_KEY': CIPHERS['key']})
        self.env.start(); self.addCleanup(self.env.stop)
        self.tmp = tempfile.TemporaryDirectory(); self.addCleanup(self.tmp.cleanup)
        self.template = Path(self.tmp.name) / 'policy.json'; self.template.write_text(json.dumps(POLICY))

    def build(self):
        bundle, _, _ = portal.build(self.db, *self.inputs, now=1800000000)
        core, mapping, _, routes, hosts = self.inputs
        subscriptions.extend(self.db, bundle, core, mapping, routes, hosts, self.template)
        return bundle

    def test_saved_group_order_ignores_retired_groups_and_appends_new_template_groups(self):
        self.db.execute('CREATE TABLE subscription_group_order(id INTEGER PRIMARY KEY, group_names TEXT)')
        self.db.execute('INSERT INTO subscription_group_order VALUES (1,?)', (json.dumps(['AI', 'retired group', 'PROXY']),))
        document = json.loads(self.template.read_text())
        document['proxy-groups'].append({'name': 'New group', 'type': 'select', 'proxies': ['PROXY']})
        self.template.write_text(json.dumps(document))
        extension = self.build()['subscriptions_extension']
        names = [group['name'] for group in document['proxy-groups']]
        expected = [name for name in ['AI', 'PROXY'] if name in names]
        expected += [name for name in names if name not in expected]
        self.assertEqual(extension['policy']['group_order'], expected)

    def test_shared_full_bundle_and_no_plaintext_external_credentials(self):
        got = self.build()
        expected = json.loads((ROOT / 'internal/web/service/testdata/marzban_stage4_portal.json').read_text())
        self.assertEqual(got, expected)
        self.assertEqual(got['plans'][0]['external_selections'], [{'source_id': 12, 'mode': 'selected', 'node_ids': [31]}])
        self.assertEqual(got['plans'][0]['group_members'], {'AI': ['inbound:102', 'external']})
        self.assertFalse(got['plans'][0]['compatibility_pending'])
        serialized = json.dumps(got)
        self.assertNotIn('synthetic-entry', serialized)
        self.assertNotIn('token=synthetic', serialized)
        extension = got['subscriptions_extension']
        self.assertEqual(sum(bool(r['client_stream']) for r in extension['routes']), 1)
        self.assertEqual(sum(h['isDisabled'] for h in extension['hosts']), 1)
        self.assertTrue(extension['policy']['secure_dns'])

    def test_sqlite_flags_match_source_orm_route_identity(self):
        row=dict(self.db.execute("SELECT * FROM hosts WHERE id=1").fetchone())
        cached=subscriptions.cached_host(row)
        self.assertIs(cached["allowinsecure"],False)
        self.assertIs(cached["mux_enable"],False)
        self.assertIn("29d569dd2a78f4ccd03e5487172e1f1de48f7f7cedbe8895f975dac55ad15af1", self.inputs[3]["TEST_VISION"])
        row.update(allowinsecure=None,mux_enable=None,random_user_agent=None,use_sni_as_host=None)
        cached=subscriptions.cached_host(row)
        for key in ("allowinsecure","mux_enable","random_user_agent","use_sni_as_host"):
            self.assertIsNone(cached[key])

    def test_catalog_cannot_drop_route_or_change_tls_behind_a_grant(self):
        bundle = self.build()
        core, mapping, _, routes, hosts = self.inputs
        bad_routes = deepcopy(routes);bad_routes['TEST_XHTTP'].pop(next(iter(bad_routes['TEST_XHTTP'])))
        with self.assertRaisesRegex(portal.MigrationError, 'exactly'):
            subscriptions.extend(self.db, bundle, core, mapping, bad_routes, hosts, self.template)
        bad_hosts = deepcopy(hosts);bad_hosts[0]['security'] = 'none'
        with self.assertRaisesRegex(portal.MigrationError, 'parameters differ'):
            subscriptions.extend(self.db, bundle, core, mapping, routes, bad_hosts, self.template)

    @unittest.skipUnless(importlib.util.find_spec("jinja2") and importlib.util.find_spec("yaml"), "optional template dependencies")
    def test_nested_template_uses_frozen_root_and_hashes_included_content(self):
        folder=Path(self.tmp.name)/"templates"/"clash";folder.mkdir(parents=True)
        root=folder/"private.yml";included=folder/"base.yml"
        root.write_text('{% include "clash/base.yml" %}')
        included.write_text('proxy-groups: [{name: PROXY, type: select, proxies: [__MGMA_LOCAL_NODES__]}]\nrules: ["MATCH,PROXY"]\n')
        doc,primary,secure,first_hash=subscriptions.load_policy(root)
        self.assertEqual(primary,"PROXY")
        self.assertEqual(doc["rules"],["MATCH,PROXY"])
        self.assertFalse(secure)
        included.write_text(included.read_text()+"ipv6: true\n")
        _,_,_,changed_hash=subscriptions.load_policy(root)
        self.assertNotEqual(first_hash,changed_hash)
        root.write_text('{% include "../outside.yml" %}')
        with self.assertRaises(portal.MigrationError):subscriptions.load_policy(root)

    def test_hidden_empty_historical_plan_stays_unsellable(self):
        self.db.execute("INSERT INTO subscription_plans SELECT 99,'Retired plan',description,price_minor,currency,duration_days,data_limit,'[]','{}','{}',0,created_at,updated_at FROM subscription_plans WHERE id=4")
        bundle=self.build()
        plan=next(p for p in bundle["plans"] if p["id"]==99)
        self.assertTrue(plan["compatibility_pending"])
        self.assertFalse(plan["is_visible"])
        self.assertEqual(plan["inbound_ids"],[])
        self.assertEqual(plan["external_selections"],[])

    def test_missing_source_key_is_blocked_before_preparing_bundle(self):
        with patch.dict(os.environ, {'EXTERNAL_SUBSCRIPTION_ENCRYPTION_KEY': ''}):
            with self.assertRaisesRegex(portal.MigrationError, 'source encryption key'):
                self.build()


if __name__ == '__main__':
    unittest.main()
