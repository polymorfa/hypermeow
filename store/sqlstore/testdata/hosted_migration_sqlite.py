# Copyright (c) 2026 Rajeh Taher
# Licensed under the MIT License. See LICENSE-MIT for details.

"""Exercise the production SQL text using an isolated in-memory SQLite database."""
import pathlib,re,sqlite3
s=(pathlib.Path(__file__).resolve().parents[1]/'store.go').read_text()
def query(name):return re.search(r'\b'+re.escape(name)+r'\s*=\s*`([^`]+)`',s).group(1)
db=sqlite3.connect(':memory:')
db.executescript('''
CREATE TABLE whatsmeow_sessions(our_jid TEXT, their_id TEXT, session BLOB, PRIMARY KEY(our_jid,their_id));
CREATE TABLE whatsmeow_identity_keys(our_jid TEXT, their_id TEXT, identity BLOB, PRIMARY KEY(our_jid,their_id));
CREATE TABLE whatsmeow_sender_keys(our_jid TEXT, chat_id TEXT, sender_id TEXT, sender_key BLOB, PRIMARY KEY(our_jid,chat_id,sender_id));
''')
our='15550000000:7@s.whatsapp.net';pn='15551234567_128';lid='123456789012345_129'
# The underscore wildcard previously selected another real user's address.
other='155512345670128:99';regular='15551234567:99';hosted=pn+':99';target=lid+':99'
for table,idcol,data,migrate,cleanup in [
('whatsmeow_sessions','their_id','session','migratePNToLIDSessionsQuery','deleteAllSessionsQuery'),
('whatsmeow_identity_keys','their_id','identity','migratePNToLIDIdentityKeysQuery','deleteAllIdentityKeysQuery'),
('whatsmeow_sender_keys','sender_id','sender_key','migratePNToLIDSenderKeysQuery','deleteAllSenderKeysQuery')]:
    extra=',chat_id' if idcol=='sender_id' else ''
    for address in [hosted,other,regular]:
        columns='our_jid,'+idcol+','+data+extra
        values=[our,address,address.encode()]+(['group@g.us'] if extra else [])
        db.execute(f'INSERT INTO {table}({columns}) VALUES('+','.join('?' for _ in values)+')',values)
    sql=query(migrate); args=[our,pn,lid]+([pn+':',pn+';'] if '$5' in sql else [])
    db.execute(sql,{str(i+1):value for i,value in enumerate(args)})
    sql=query(cleanup); args=[our,pn+':%'] if 'LIKE' in sql else [our,pn+':',pn+';']
    db.execute(sql,{str(i+1):value for i,value in enumerate(args)})
    rows=dict(db.execute(f'SELECT {idcol},{data} FROM {table} WHERE our_jid=?',[our]))
    assert hosted not in rows, (table,'hosted PN not removed')
    assert rows.get(target)==hosted.encode(), (table,'hosted session not migrated')
    assert rows.get(other)==other.encode(), (table,'unrelated PN deleted by wildcard')
    assert rows.get(regular)==regular.encode(), (table,'regular device99 changed')
    print(table+': hosted migration preserved unrelated PN and regular device99',flush=True)
