Attribute VB_Name = "AccessdumpQueries"
' Access side of the accessdump query tests. Import this module into an
' Access database (Alt+F11, File > Import File; saved as Windows-1252 so the
' umlauts survive the import) and run one of the two public subs from the
' Immediate window.
'
'   BuildFixture
'     Run in a NEW, EMPTY .mdb (Access 2002-2003 format). Creates tables with
'     umlauts, one saved query per query type, a form, a report and a combo box
'     with SQL record/row sources (these become ~sq_ queries), then calls
'     ExportQuerySQL. Hand back the .mdb as testdata/queries/fixture.mdb and the
'     export as testdata/queries/fixture.mdb.expected.txt.
'
'   ExportQuerySQL
'     Run in ANY database, e.g. a production frontend. Writes the SQL Access
'     itself shows for every QueryDef (CurrentDb.QueryDefs("x").SQL), embedded
'     ~sq_ queries included, to <database>.expected.txt next to the database.
'     Passwords in connect strings are replaced by *** before writing.
'
' Output format, UTF-8, one block per query:
'   ### QUERY <name>
'   ### TYPE <QueryDef.Type>
'   ### CONNECT <connect string, redacted>     (only if not empty)
'   <SQL, as many lines as it has>
Option Compare Database
Option Explicit

Public Sub BuildFixture()
    Dim db As DAO.Database
    Set db = CurrentDb

    CreateTables db
    CreateQueries db
    CreateFormsAndReports

    ExportQuerySQL
End Sub

Private Sub CreateTables(db As DAO.Database)
    db.Execute "CREATE TABLE Kunden (ID COUNTER PRIMARY KEY, Name TEXT(100), [Straße Nr] TEXT(100), Aktiv YESNO)"
    db.Execute "CREATE TABLE Aufträge (Nr LONG PRIMARY KEY, KundeID LONG, Datum DATETIME, Erledigt YESNO)"
    db.Execute "CREATE TABLE Positionen (ID COUNTER PRIMARY KEY, AuftragNr LONG, ArtikelNr LONG, Menge DOUBLE, Betrag CURRENCY)"
    db.Execute "CREATE TABLE Artikel (Nr LONG PRIMARY KEY, Bezeichnung TEXT(100), Preis CURRENCY, Größe TEXT(20))"
    db.Execute "CREATE TABLE Preise (Nr LONG PRIMARY KEY, Neu CURRENCY, Gültig YESNO)"
    db.Execute "CREATE TABLE Material (ID LONG PRIMARY KEY, Beistellung TEXT(50))"
    db.Execute "CREATE TABLE Umsatz (Kunde TEXT(100), Monat TEXT(7), Betrag CURRENCY)"
    db.Execute "CREATE TABLE Archiv (Nr LONG, [Archiviert am] DATETIME)"
    db.Execute "CREATE TABLE Log ([Text] TEXT(255), Zeit DATETIME)"
End Sub

Private Sub CreateQueries(db As DAO.Database)
    Dim qd As DAO.QueryDef

    ' Select: LEFT and RIGHT join over three tables, umlauts, alias.
    db.CreateQueryDef "qrySelectJoins", _
        "SELECT Aufträge.Nr, Kunden.[Straße Nr] AS Straße, Positionen.Betrag " & _
        "FROM (Kunden LEFT JOIN Aufträge ON Kunden.ID = Aufträge.KundeID) " & _
        "RIGHT JOIN Positionen ON Aufträge.Nr = Positionen.AuftragNr;"

    ' Select: join on two columns.
    db.CreateQueryDef "qryJoinTwoColumns", _
        "SELECT * FROM Artikel INNER JOIN Preise ON (Artikel.Nr = Preise.Nr) AND (Artikel.Preis = Preise.Neu);"

    ' Select: parameters, subquery in WHERE, DLookup, GROUP BY, HAVING, ORDER BY DESC.
    db.CreateQueryDef "qryParameter", _
        "PARAMETERS [Ab Datum] DateTime, Kunde Text ( 255 ); " & _
        "SELECT DISTINCT A.KundeID, DLookUp(""Beistellung"",""Material"",""ID="" & [A].[KundeID]) AS Beistellung, " & _
        "Sum(P.Betrag) AS Summe " & _
        "FROM Aufträge AS A INNER JOIN Positionen AS P ON A.Nr = P.AuftragNr " & _
        "WHERE A.Datum >= [Ab Datum] AND A.KundeID IN (SELECT ID FROM Kunden WHERE Name = [Kunde]) " & _
        "GROUP BY A.KundeID " & _
        "HAVING Sum(P.Betrag) > 0 " & _
        "ORDER BY Sum(P.Betrag) DESC, A.KundeID;"

    ' Select: TOP n PERCENT, DISTINCTROW, query on a query.
    db.CreateQueryDef "qryTopPercent", "SELECT TOP 10 PERCENT * FROM Kunden ORDER BY Name;"
    db.CreateQueryDef "qryDistinctRow", "SELECT DISTINCTROW Kunden.Name FROM Kunden INNER JOIN Aufträge ON Kunden.ID = Aufträge.KundeID;"
    db.CreateQueryDef "qryOnQuery", "SELECT qrySelectJoins.Nr FROM qrySelectJoins WHERE qrySelectJoins.Betrag > 100;"

    ' Make-table.
    db.CreateQueryDef "qryMakeTable", "SELECT * INTO [tmp Export] FROM Kunden WHERE Aktiv = True;"

    ' Append from a select, append values.
    db.CreateQueryDef "qryAppendSelect", _
        "INSERT INTO Archiv ( Nr, [Archiviert am] ) SELECT Aufträge.Nr, Date() FROM Aufträge WHERE Aufträge.Erledigt;"
    db.CreateQueryDef "qryAppendValues", "INSERT INTO Log ( [Text], Zeit ) VALUES ('Start', Now());"

    ' Update over a join.
    db.CreateQueryDef "qryUpdate", _
        "UPDATE Artikel INNER JOIN Preise ON Artikel.Nr = Preise.Nr SET Artikel.Preis = [Preise].[Neu] WHERE Preise.Gültig;"

    ' Delete.
    db.CreateQueryDef "qryDelete", "DELETE Aufträge.* FROM Aufträge WHERE Aufträge.Datum < Date()-365;"

    ' Crosstab.
    db.CreateQueryDef "qryCrosstab", _
        "TRANSFORM Sum(Umsatz.Betrag) AS Summe SELECT Umsatz.Kunde FROM Umsatz GROUP BY Umsatz.Kunde PIVOT Umsatz.Monat;"

    ' Union and union all.
    db.CreateQueryDef "qryUnion", "SELECT Nr FROM Aufträge UNION SELECT Nr FROM Archiv;"
    db.CreateQueryDef "qryUnionAll", "SELECT Name FROM Kunden UNION ALL SELECT Bezeichnung FROM Artikel ORDER BY Name;"

    ' Data definition.
    db.CreateQueryDef "qryDDL", "CREATE INDEX ixUmsatzKunde ON Umsatz (Kunde);"

    ' Pass-through. The server does not have to exist.
    Set qd = db.CreateQueryDef("qryPassThrough")
    qd.Connect = "ODBC;DRIVER={SQL Server};SERVER=gibtsnicht;DATABASE=IPOffice;UID=app;PWD=geheim"
    qd.ReturnsRecords = True
    qd.SQL = "EXEC dbo.Abrechnung 2026"
End Sub

Private Sub CreateFormsAndReports()
    Dim frm As Form
    Dim ctl As Control
    Dim rpt As Report
    Dim tmpName As String

    ' Form with an SQL record source and a combo box with an SQL row source:
    ' ~sq_ffrmAuftrag and ~sq_cfrmAuftrag~sq_ccboKunde.
    Set frm = CreateForm()
    frm.RecordSource = "SELECT Aufträge.* FROM Aufträge WHERE Aufträge.Erledigt = False;"
    Set ctl = CreateControl(frm.Name, acComboBox, acDetail)
    ctl.Name = "cboKunde"
    ctl.RowSourceType = "Table/Query"
    ctl.RowSource = "SELECT Kunden.ID, Kunden.Name FROM Kunden ORDER BY Kunden.Name;"
    tmpName = frm.Name
    DoCmd.Close acForm, tmpName, acSaveYes
    DoCmd.Rename "frmAuftrag", acForm, tmpName

    ' Report with an SQL record source: ~sq_rrptUmsatz.
    Set rpt = CreateReport()
    rpt.RecordSource = "SELECT Umsatz.Kunde, Sum(Umsatz.Betrag) AS Gesamt FROM Umsatz GROUP BY Umsatz.Kunde;"
    tmpName = rpt.Name
    DoCmd.Close acReport, tmpName, acSaveYes
    DoCmd.Rename "rptUmsatz", acReport, tmpName
End Sub

Public Sub ExportQuerySQL()
    Dim db As DAO.Database
    Dim qd As DAO.QueryDef
    Dim out As Object
    Dim path As String

    Set db = CurrentDb
    path = db.Name & ".expected.txt"

    Set out = CreateObject("ADODB.Stream")
    out.Type = 2 ' text
    out.Charset = "utf-8"
    out.Open

    For Each qd In db.QueryDefs
        out.WriteText "### QUERY " & qd.Name & vbLf
        out.WriteText "### TYPE " & qd.Type & vbLf
        If Len(qd.Connect) > 0 Then
            out.WriteText "### CONNECT " & RedactPasswords(qd.Connect) & vbLf
        End If
        out.WriteText Replace(RedactPasswords(qd.SQL), vbCrLf, vbLf) & vbLf
    Next qd

    out.SaveToFile path, 2 ' overwrite
    out.Close

    Debug.Print "Wrote " & db.QueryDefs.Count & " queries to " & path
End Sub

' Replaces the value of every PWD= / Password= with ***.
Private Function RedactPasswords(ByVal s As String) As String
    Dim re As Object
    Set re = CreateObject("VBScript.RegExp")
    re.Global = True
    re.IgnoreCase = True
    re.Pattern = "\b(PWD|PASSWORD)(\s*=\s*)(\{[^}]*\}|[^;'""\]\r\n]*)"
    RedactPasswords = re.Replace(s, "$1$2***")
End Function
