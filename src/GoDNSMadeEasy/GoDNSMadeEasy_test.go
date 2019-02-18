package GoDNSMadeEasy

import (
	"flag"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	apiKey          = flag.String("APIKey", "", "Your DNS Made Easy Sandbox API Key")
	secretKey       = flag.String("SecretKey", "", "Your DNS Made Easy Sandbox Secret Key")
	purgeAllDomains = flag.Bool("PurgeAll", false, "Delete every domain matching gotest-* in the account before running any tests. Useful if you have a bunch of failed tests and want to clear it all out.")
	timeAdjust      = flag.Int("TimeOffset", 0, "Timestamp adjustment in seconds. DNS Made Easy has a very strict time synchronisation requirement. If your local clock runs slightly fast or slow (even by 30 seconds), requests will fail. You can adjust the timestamp sent by DNS Made Easy here to account for this offset")
	DomainsCreated  = make(map[string]*Domain)
)

func TestMain(m *testing.M) {
	flag.Parse()
	if *purgeAllDomains {
		doThePurge()
	}
	m.Run()
	cleanUpDomains()
}

// TestCreateDomain tests the creation of a domain. This is kind of a redundant test, because every other test is going to fail
// if we can't do this.
func TestCreateDomain(t *testing.T) {
	DMEClient, err := newClient()
	if err != nil {
		t.Fatal(err)
	}
	newDomain, err := generateTestDomain(DMEClient)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Using test domain name", newDomain.Name)

	if newDomain.ID == 0 {
		t.Fatal("domain ID is 0")
	}
}

// TestCreateDomain tests the creation of a domain. This is kind of a redundant test, because every other test is going to fail
// if we can't do this.
func TestRecords(t *testing.T) {
	var TestRecords = getTestRecords(false)
	var UpdateRecords = getTestRecords(true)
	var CreatedRecords []*Record

	DMEClient, err := newClient()
	if err != nil {
		t.Fatal(err)
	}
	newDomain, err := generateTestDomain(DMEClient)
	if err != nil {
		t.Fatal(err)
	}
	DomainID := newDomain.ID
	t.Log("Using test domain name", newDomain.Name)

	//Create a record of each type
	for _, thisRecord := range TestRecords {
		newRecord, err := DMEClient.AddRecord(DomainID, &thisRecord)
		if err != nil {
			t.Error(fmt.Sprintf("%s: %s", thisRecord.Name, err))
		}
		mismatches := compareRecords(&thisRecord, newRecord)
		if len(mismatches) > 0 {
			t.Error(fmt.Sprintf("(create) %s %s: records do not match: %s", thisRecord.Type, thisRecord.Name, strings.Join(mismatches, ",")))
		}
		CreatedRecords = append(CreatedRecords, newRecord)
	}

	//Update previously created records
	for _, thisRecord := range UpdateRecords {
		for _, existingRecord := range CreatedRecords {
			if thisRecord.Type == existingRecord.Type && thisRecord.Name == existingRecord.Name {
				thisRecord.ID = existingRecord.ID
				err := DMEClient.UpdateRecord(DomainID, &thisRecord)
				if err != nil {
					t.Error(fmt.Sprintf("(update) %s %s: %s", thisRecord.Type, thisRecord.Name, err))
				}
				//Because DNS Made Easy does not return the new record, and doesn't give a method for retrieving a single record, this is
				//all we can do here
			}
		}
	}

	//And delete ther records we just updated. This also tests the mass delete function
	var recordsToDelete []int
	for _, existingRecord := range CreatedRecords {
		recordsToDelete = append(recordsToDelete, existingRecord.ID)
	}
	err = DMEClient.DeleteRecords(DomainID, recordsToDelete)
	if err != nil {
		t.Error(fmt.Sprintf("(delete): %s", err))
	}

}

// TestDomainRead will create a domain, then query for it in two ways: by a direct ID query, and then by looking for it in the complete
// list of domains returned by DNS Made Easy
func TestDomainRead(t *testing.T) {
	DMEClient, err := newClient()
	if err != nil {
		t.Fatal(err)
	}
	newDomain, err := generateTestDomain(DMEClient)
	if err != nil {
		t.Fatal(err)
	}
	newDomainID := newDomain.ID
	t.Log("Using test domain name", newDomain.Name)

	//See if we can retrieve this domain directly
	fetchDirect, err := DMEClient.Domain(newDomainID)
	if err != nil {
		t.Error("direct fetch error: ", err)
	}
	if fetchDirect == nil {
		t.Fatal("direct fetch of domain is nil")
	}
	if fetchDirect.ID != newDomainID {
		t.Errorf("direct fetch domain IDs do not match (%v, %v)", newDomain, fetchDirect.ID)
	}

	fullDomainList, err := DMEClient.Domains()
	if err != nil {
		t.Error("full domain fetch error: ", err)
	}
	if len(fullDomainList) == 0 {
		t.Error("full domain fetch returned 0 records")
	}

	var foundOurDomain bool
	for _, thisDomain := range fullDomainList {
		if thisDomain.ID == newDomainID {
			foundOurDomain = true
			break
		}
	}
	if !foundOurDomain {
		t.Errorf("full domain fetch returned %v records but none of them was our domain", len(fullDomainList))
	}

}

// TestVanity creates a vanity NS set, checks that it was created, and then assigns it to a domain
func TestVanity(t *testing.T) {
	DMEClient, err := newClient()
	if err != nil {
		t.Fatal(err)
	}

	newVanity := &Vanity{
		Name:              fmt.Sprintf("testvanity-%v", time.Now().UnixNano()),
		Servers:           []string{"ns1.example.org", "ns2.example.org", "ns3.example.org", "ns4.example.org", "ns5.example.org"},
		NameServerGroupID: 1,
	}

	addedVanity, err := DMEClient.AddVanity(*newVanity)
	if err != nil {
		t.Fatal(err)
	}
	newVanityID := addedVanity.ID

	allVanities, err := DMEClient.Vanity()
	if err != nil {
		t.Error(err)
	}

	var foundVanity bool
	for _, thisVanity := range allVanities {
		if thisVanity.ID == newVanityID {
			foundVanity = true
			break
		}
	}

	if !foundVanity {
		t.Error("could not find our new vanity in vanity list")
	}

	//Assign vanity to a domain
	newDomain, err := generateTestDomain(DMEClient)
	if err != nil {
		t.Fatal(err)
	}
	newDomain.VanityID = newVanityID
	err = DMEClient.UpdateDomain(newDomain)
	if err != nil {
		t.Error(err)
	}

	//Check that the vanity actually applied
	fetchedDomain, err := DMEClient.Domain(newDomain.ID)
	if err != nil {
		t.Error(err)
	}
	if fetchedDomain.VanityID != newVanityID {
		t.Errorf("Vanity IDs on domain do not match (%v %v)", fetchedDomain.VanityID, newVanityID)
	}
}

// TestSOA creates an SOA, updates it, then deletes it
func TestSOA(t *testing.T) {
	DMEClient, err := newClient()
	if err != nil {
		t.Fatal(err)
	}

	newSOA := SOA{
		Name:          fmt.Sprintf("testsoa-%v", time.Now().UnixNano()),
		Comp:          "test.example.org",
		Email:         "test.example.org",
		TTL:           21600,
		Serial:        1337,
		Refresh:       86400,
		Retry:         300,
		Expire:        86400,
		NegativeCache: 600,
	}

	createdSOA, err := DMEClient.AddSOA(newSOA)
	if err != nil {
		t.Fatal(err)
	}

	createdSOA.Name += "updated"
	err = DMEClient.UpdateSOA(createdSOA)
	if err != nil {
		t.Error(err)
	}

	err = DMEClient.DeleteSOA(createdSOA.ID)
	if err != nil {
		t.Error(err)
	}
}

// TestIPSets creates an IP Set, updates it, then deletes it
func TestIPSets(t *testing.T) {
	DMEClient, err := newClient()
	if err != nil {
		t.Fatal(err)
	}

	thisIPSet := IPSet{
		Name: fmt.Sprintf("testipset-%v", time.Now().UnixNano()),
		Ips:  []string{"127.0.0.1", "127.0.0.2", "127.0.0.3"},
	}
	newIPSet, err := DMEClient.AddIPSet(thisIPSet)
	if err != nil {
		t.Fatal(err)
	}
	newIPSetID := newIPSet.ID

	existingIPSets, err := DMEClient.IPSets()
	if err != nil {
		t.Error(err)
	}

	var foundThisIPSet bool
	for _, thisIPSet := range existingIPSets {
		if thisIPSet.ID == newIPSetID {
			foundThisIPSet = true
			break
		}
	}

	if !foundThisIPSet {
		t.Errorf("unable to locate new IPSet in existing sets")
	}

	newIPSet.Name = newIPSet.Name + "updated"
	err = DMEClient.UpdateIPSet(newIPSet)
	if err != nil {
		t.Error(err)
	}

	err = DMEClient.DeleteIPSet(newIPSet.ID)
	if err != nil {
		t.Error(err)
	}

}

func TestSecondaryDomain(t *testing.T) {
	DMEClient, err := newClient()
	if err != nil {
		t.Fatal(err)
	}

	//To add a secondary domain, we need to specify an IPSet
	newIPSet, err := DMEClient.AddIPSet(IPSet{
		Name: fmt.Sprintf("testipset-%v", time.Now().UnixNano()),
		Ips:  []string{"127.0.0.1", "127.0.0.2", "127.0.0.3"},
	})
	if err != nil {
		t.Fatal(err)
	}

	folderList, err := DMEClient.Folders()
	if err != nil {
		t.Fatal(err)
	}
	if folderList == nil {
		t.Fatal("unable to retrieve folder list")
	}

	thisDomainName := fmt.Sprintf("gotest-%v.org", time.Now().UnixNano())
	newSecondaryDomain, err := DMEClient.AddSecondaryDomain(SecondaryDomain{
		Name:     thisDomainName,
		IPSetID:  newIPSet.ID,
		FolderID: folderList[0].Value,
	})
	if err != nil {
		t.Fatal(err)
	}
	if newSecondaryDomain == nil {
		t.Fatal("new secondary domain is nil")
	}
	if newSecondaryDomain.ID == 0 {
		t.Error("new secondary domain has 0 ID")
	}

	//Check that our domain is in the domain list
	allSecondaryDomains, err := DMEClient.SecondaryDomains()
	if err != nil {
		t.Error(err)
	}

	var foundSecondaryDomain bool
	for _, thisSecondaryDomain := range allSecondaryDomains {
		if thisSecondaryDomain.ID == newSecondaryDomain.ID {
			foundSecondaryDomain = true
			break
		}
	}
	if !foundSecondaryDomain {
		t.Error("could not locate secondary domain in domain list")
	}

	DMEClient.DeleteSecondaryDomain(newSecondaryDomain.ID, 2*time.Minute)
	DMEClient.DeleteIPSet(newIPSet.ID)

}

// TestExportAll runs the ExportAllDomains() function and sees if it returns any errors. That's about it.
func TestExportAll(t *testing.T) {
	DMEClient, err := newClient()
	if err != nil {
		t.Fatal(err)
	}
	_, err = DMEClient.ExportAllDomains()
	if err != nil {
		t.Error(err)
	}
}

//Create a test domain, return the domain entry for this domain, and add it to our list of domains that needs to be cleaned up at the end
//Names are generated using a timestamp.
func generateTestDomain(DMEClient *GoDMEConfig) (*Domain, error) {
	thisDomainName := fmt.Sprintf("gotest-%v.org", time.Now().UnixNano())
	newDomain, err := DMEClient.AddDomain(&Domain{
		Name: thisDomainName,
	})
	if err != nil {
		return nil, err
	}

	DomainsCreated[thisDomainName] = newDomain
	return newDomain, nil
}

//We need to clean up after our tests are run, so we don't leave old domains lying around in the sandbox
func cleanUpDomains() {
	fmt.Println("Cleaning up domains...")
	//Create a client for talking to DME
	DMEClient, err := newClient()
	if err != nil {
		fmt.Println(err)
		return
	}

	//Create a WaitGroup, so we can delete the domains in parallel, but wait for all to complete
	var wg sync.WaitGroup
	for name, domain := range DomainsCreated { //Loop through the domains we created during this testing
		wg.Add(1)                              //Add one to the wait group
		go func(name string, domain *Domain) { //Delete the domains asynchronously
			defer wg.Done()                                         //When this is finished, indicate to the Wait Group that we're done
			fmt.Println("Deleting", name)                           //Send something to console so we know what's going on
			err := DMEClient.DeleteDomain(domain.ID, 2*time.Minute) //Delete the domain, with a 2 minute timeout. Sandbox takes around 50 seconds on average
			if err != nil {
				fmt.Println("Could not delete", name, "error:", err)
			}
		}(name, domain)
	}
	wg.Wait() //Wait for all the Done()'s to come through
}

//Create a DNS Made Easy client for each test to run from, as they are run in parallel
func newClient() (*GoDMEConfig, error) {
	return NewGoDNSMadeEasy(&GoDMEConfig{
		APIKey:               *apiKey,
		SecretKey:            *secretKey,
		APIUrl:               SANDBOXAPI,
		DisableSSLValidation: true,
		TimeAdjust:           (time.Duration(*timeAdjust) * time.Second),
	})

}

func getTestRecords(Updated bool) []Record {
	recIPVal, recTTL, recIPv6Val, recDomain, recData := "127.8.4.3", 300, "::1", "example.org.", "\"originalvalue\""

	if Updated {
		recIPVal, recTTL, recIPv6Val, recDomain, recData = "10.85.67.244", 1800, "::BEEF", "example.com.", "\"newvalue\""
	}
	var TestRecords []Record

	//Gimmie an A
	TestRecords = append(TestRecords, Record{
		Name:        "testa",
		Type:        "A",
		Value:       recIPVal,
		TTL:         recTTL,
		GtdLocation: "DEFAULT",
	})

	//Gimmie an AAAA
	TestRecords = append(TestRecords, Record{
		Name:        "testaaaa",
		Type:        "AAAA",
		Value:       recIPv6Val,
		TTL:         recTTL,
		GtdLocation: "DEFAULT",
	})

	//Gimmie a CNAME
	TestRecords = append(TestRecords, Record{
		Name:        "testcname",
		Type:        "CNAME",
		Value:       recDomain,
		TTL:         recTTL,
		GtdLocation: "DEFAULT",
	})

	//Gimmie a ANAME
	TestRecords = append(TestRecords, Record{
		Name:        "",
		Type:        "ANAME",
		Value:       recDomain,
		TTL:         recTTL,
		GtdLocation: "DEFAULT",
	})

	//Gimmie a MX
	TestRecords = append(TestRecords, Record{
		Name:        "testmx",
		Type:        "MX",
		Value:       recDomain,
		TTL:         recTTL,
		MxLevel:     ptrInt(10),
		GtdLocation: "DEFAULT",
	})

	//Gimmie a HTTP
	TestRecords = append(TestRecords, Record{
		Name:         "testred",
		Type:         "HTTPRED",
		Value:        strings.TrimSuffix(fmt.Sprintf("http://%s", recDomain), "."),
		TTL:          recTTL,
		HardLink:     false,
		RedirectType: "STANDARD - 301",
		Title:        "test redirect title",
		Keywords:     "just,stuff",
		Description:  "just doin some stuff",
		GtdLocation:  "DEFAULT",
	})

	//Gimmie a TXT
	TestRecords = append(TestRecords, Record{
		Name:        "testtxt",
		Type:        "TXT",
		Value:       recData,
		TTL:         recTTL,
		GtdLocation: "DEFAULT",
	})

	//Gimmie a SPF
	TestRecords = append(TestRecords, Record{
		Name:        "testtxt",
		Type:        "SPF",
		Value:       recData,
		TTL:         recTTL,
		GtdLocation: "DEFAULT",
	})

	//Gimmie a PTR. Yeah I know this isn't a useful PTR record, but we can still test with it
	TestRecords = append(TestRecords, Record{
		Name:        "testptr",
		Type:        "PTR",
		Value:       recDomain,
		TTL:         recTTL,
		GtdLocation: "DEFAULT",
	})

	//Gimmie a NS
	TestRecords = append(TestRecords, Record{
		Name:        "testns",
		Type:        "NS",
		Value:       recDomain,
		TTL:         recTTL,
		GtdLocation: "DEFAULT",
	})

	//Gimmie a SRV
	TestRecords = append(TestRecords, Record{
		Name:        "_testsrv",
		Type:        "SRV",
		Priority:    ptrInt(10),
		Weight:      ptrInt(10),
		Port:        80,
		Value:       recDomain,
		TTL:         recTTL,
		GtdLocation: "DEFAULT",
	})

	return TestRecords
}

func compareRecords(a, b *Record) []string {

	var mismatches []string
	if a == nil || b == nil {
		if a == nil {
			mismatches = append(mismatches, "A is nil")
		}

		if a == nil {
			mismatches = append(mismatches, "B is nil")
		}
		return mismatches
	}

	//All records have a name, a type, a value,  a TTL and a GtdLocation
	if a.Type != b.Type {
		mismatches = append(mismatches, "Type")
	}
	if a.Name != b.Name {
		mismatches = append(mismatches, "Name")
	}
	if a.Value != b.Value {
		mismatches = append(mismatches, "Value")
	}
	if a.TTL != b.TTL {
		mismatches = append(mismatches, "TTL")
	}
	if a.GtdLocation != b.GtdLocation {
		mismatches = append(mismatches, "GtdLocation")
	}

	//But some have more
	switch a.Type {
	case "MX":
		if a.MxLevel != b.MxLevel {
			mismatches = append(mismatches, "MxLevel")
		}

	case "HTTP":
		if a.HardLink != b.HardLink {
			mismatches = append(mismatches, "HardLink")
		}

		if a.Title != b.Title {
			mismatches = append(mismatches, "Title")
		}

		if a.Keywords != b.Keywords {
			mismatches = append(mismatches, "Keywords")
		}

		if a.Description != b.Description {
			mismatches = append(mismatches, "Description")
		}
	case "SRV":
		if a.Weight != b.Weight {
			mismatches = append(mismatches, "Weight")
		}
		if a.Port != b.Port {
			mismatches = append(mismatches, "Port")
		}
		if a.Priority != b.Priority {
			mismatches = append(mismatches, "Priority")
		}
	}
	return mismatches
}

func doThePurge() error {
	DMEClient, err := newClient()
	if err != nil {
		return err
	}

	domains, err := DMEClient.Domains()
	if err != nil {
		return err
	}

	TestDomainMatch := regexp.MustCompile("^gotest-\\d+\\.org$")

	var wg sync.WaitGroup
	for _, thisDomain := range domains { //Loop through the domains we created during this testing
		if !TestDomainMatch.MatchString(thisDomain.Name) {
			fmt.Println("Skipping", thisDomain.Name)
			continue
		}

		wg.Add(1)                   //Add one to the wait group
		go func(delDomain Domain) { //Delete the domains asynchronously
			defer wg.Done()                         //When this is finished, indicate to the Wait Group that we're done
			fmt.Println("Deleting", delDomain.Name) //Send something to console so we know what's going on

			err := DMEClient.DeleteDomain(delDomain.ID, 2*time.Minute) //Delete the domain, with a 2 minute timeout. Sandbox takes around 50 seconds on average
			if err != nil {
				fmt.Println("Could not delete", delDomain.Name, "error:", err)
			}

		}(thisDomain)
	}
	wg.Wait() //Wait for all the Done()'s to come through

	return nil
}

func ptrInt(i int) *int {
	return &i
}
