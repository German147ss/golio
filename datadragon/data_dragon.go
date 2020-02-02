// Package datadragon provides methods for retrieving data from the DataDragon API.
// This data is only updated for every new version of League of Legends.
package datadragon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/KnutZuidema/golio/api"
	"github.com/KnutZuidema/golio/internal"
	"github.com/KnutZuidema/golio/model"

	log "github.com/sirupsen/logrus"
)

const (
	latestRuneAndMasteryVersion = "7.23.1"
	fallbackVersion             = "9.10.1"
	fallbackLanguage            = LanguageCodeUnitedStates
)

var (
	regionToRealmRegion = map[api.Region]string{
		api.RegionEuropeWest:        "euw",
		api.RegionEuropeNorthEast:   "eun",
		api.RegionJapan:             "jp",
		api.RegionKorea:             "kr",
		api.RegionLatinAmericaNorth: "lan",
		api.RegionLatinAmericaSouth: "las",
		api.RegionNorthAmerica:      "na",
		api.RegionOceania:           "oce",
		api.RegionPBE:               "pbe",
		api.RegionRussia:            "ru",
		api.RegionTurkey:            "tr",
		api.RegionBrasil:            "br",
	}
)

// Client provides access to all data provided by the Data Dragon service
type Client struct {
	logger             log.FieldLogger
	Version            string
	Language           languageCode
	client             internal.Doer
	championsMu        sync.RWMutex
	championsByName    map[string]model.ChampionDataExtended
	getChampionsToggle uint32
	profileIconsMu     sync.RWMutex
	profileIcons       []model.ProfileIcon
	itemsMu            sync.RWMutex
	items              []model.Item
	masteriesMu        sync.RWMutex
	masteries          []model.Mastery
	runesMu            sync.RWMutex
	runes              []model.Item
	summonersMu        sync.RWMutex
	summoners          []model.SummonerSpell
}

// NewClient returns a new client for the Data Dragon service.
func NewClient(client internal.Doer, region api.Region, logger log.FieldLogger) *Client {
	c := &Client{
		client:          client,
		logger:          logger.WithField("client", "data dragon"),
		championsByName: map[string]model.ChampionDataExtended{},
	}
	if err := c.init(regionToRealmRegion[region]); err != nil {
		c.Version = fallbackVersion
		c.Language = fallbackLanguage
	}
	return c
}

func (c *Client) init(region string) error {
	var res struct {
		Version  string `json:"v"`
		Language string `json:"l"`
	}
	response, err := c.doRequest(dataDragonBaseURL, fmt.Sprintf("/realms/%s.json", region))
	if err != nil {
		return err
	}
	if response.Body == nil {
		return fmt.Errorf("no response body")
	}
	if err := json.NewDecoder(response.Body).Decode(&res); err != nil {
		return err
	}
	c.Version = res.Version
	c.Language = languageCode(res.Language)
	return nil
}

// GetChampions returns all existing champions
func (c *Client) GetChampions() ([]model.ChampionData, error) {
	unlock, toggle := internal.RWLockToggle(&c.championsMu)
	defer unlock()
	if atomic.CompareAndSwapUint32(&c.getChampionsToggle, 0, 1) {
		toggle()
		var champions map[string]model.ChampionData
		if err := c.getInto("/champion.json", &champions); err != nil {
			return nil, err
		}
		for _, champion := range champions {
			data := model.ChampionDataExtended{ChampionData: champion}
			c.championsByName[champion.Name] = data
		}
	}
	res := make([]model.ChampionData, 0, len(c.championsByName))
	for _, champion := range c.championsByName {
		res = append(res, champion.ChampionData)
	}
	return res, nil
}

// GetChampion returns information about the champion with the given name
func (c *Client) GetChampion(name string) (model.ChampionDataExtended, error) {
	unlock, toggle := internal.RWLockToggle(&c.championsMu)
	defer unlock()
	champion, ok := c.championsByName[name]
	if !ok || champion.Lore == "" {
		toggle()
		var data map[string]model.ChampionDataExtended
		if err := c.getInto(fmt.Sprintf("/champion/%s.json", name), &data); err != nil {
			return model.ChampionDataExtended{}, err
		}
		champion, ok = data[name]
		if !ok {
			return model.ChampionDataExtended{}, fmt.Errorf("no data for champion %s", name)
		}
		c.championsByName[name] = champion
	}
	return champion, nil
}

// GetProfileIcons returns all existing profile icons
func (c *Client) GetProfileIcons() ([]model.ProfileIcon, error) {
	unlock, toggle := internal.RWLockToggle(&c.profileIconsMu)
	defer unlock()
	if len(c.profileIcons) < 1 {
		toggle()
		var res map[string]model.ProfileIcon
		if err := c.getInto("/profileicon.json", &res); err != nil {
			return nil, err
		}
		c.profileIcons = make([]model.ProfileIcon, 0, len(res))
		for _, profileIcon := range res {
			c.profileIcons = append(c.profileIcons, profileIcon)
		}
	}
	res := make([]model.ProfileIcon, len(c.profileIcons))
	copy(res, c.profileIcons)
	return res, nil
}

// GetItems returns all existing items
func (c *Client) GetItems() ([]model.Item, error) {
	unlock, toggle := internal.RWLockToggle(&c.itemsMu)
	defer unlock()
	if len(c.items) < 1 {
		toggle()
		var res map[string]model.Item
		if err := c.getInto("/item.json", &res); err != nil {
			return nil, err
		}
		c.items = make([]model.Item, 0, len(res))
		for id, item := range res {
			item.ID = id
			c.items = append(c.items, item)
		}
	}
	res := make([]model.Item, len(c.items))
	copy(res, c.items)
	return res, nil
}

// GetMasteries returns all existing masteries. Masteries were removed in patch 7.23.1. If any version higher than that
// is specified the last available version will be used instead.
func (c *Client) GetMasteries() ([]model.Mastery, error) {
	unlock, toggle := internal.RWLockToggle(&c.masteriesMu)
	defer unlock()
	if len(c.masteries) < 1 {
		toggle()
		var res map[string]model.Mastery
		if err := c.getInto("/mastery.json", &res); err != nil {
			return nil, err
		}
		c.masteries = make([]model.Mastery, 0, len(res))
		for _, mastery := range res {
			c.masteries = append(c.masteries, mastery)
		}
	}
	res := make([]model.Mastery, len(c.masteries))
	copy(res, c.masteries)
	return res, nil
}

// GetRunes returns all existing runes. Runes were removed in patch 7.23.1. If any version higher than that
// is specified the last available version will be used instead.
func (c *Client) GetRunes() ([]model.Item, error) {
	unlock, toggle := internal.RWLockToggle(&c.runesMu)
	defer unlock()
	if len(c.runes) < 1 {
		toggle()
		var res map[string]model.Item
		if err := c.getInto("/rune.json", &res); err != nil {
			return nil, err
		}
		c.runes = make([]model.Item, 0, len(res))
		for id, runeItem := range res {
			runeItem.ID = id
			c.runes = append(c.runes, runeItem)
		}
	}
	res := make([]model.Item, len(c.runes))
	copy(res, c.runes)
	return res, nil
}

// GetSummonerSpells returns all existing summoner spells
func (c *Client) GetSummonerSpells() ([]model.SummonerSpell, error) {
	unlock, toggle := internal.RWLockToggle(&c.summonersMu)
	defer unlock()
	if len(c.summoners) < 1 {
		toggle()
		var res map[string]model.SummonerSpell
		if err := c.getInto("/summoner.json", &res); err != nil {
			return nil, err
		}
		c.summoners = make([]model.SummonerSpell, 0, len(res))
		for _, summoner := range res {
			c.summoners = append(c.summoners, summoner)
		}
	}
	res := make([]model.SummonerSpell, len(c.summoners))
	copy(res, c.summoners)
	return res, nil
}

// ClearCaches resets all caches of the data dragon client
func (c *Client) ClearCaches() {
	c.championsMu.Lock()
	c.championsByName = map[string]model.ChampionDataExtended{}
	atomic.StoreUint32(&c.getChampionsToggle, 0)
	c.championsMu.Unlock()
	c.masteriesMu.Lock()
	c.masteries = []model.Mastery{}
	c.masteriesMu.Unlock()
	c.profileIconsMu.Lock()
	c.profileIcons = []model.ProfileIcon{}
	c.profileIconsMu.Unlock()
	c.itemsMu.Lock()
	c.items = []model.Item{}
	c.itemsMu.Unlock()
	c.summonersMu.Lock()
	c.summoners = []model.SummonerSpell{}
	c.summonersMu.Unlock()
	c.runesMu.Lock()
	c.runes = []model.Item{}
	c.runesMu.Unlock()
}

func (c *Client) getInto(endpoint string, target interface{}) error {
	response, err := c.doRequest(dataDragonDataURLFormat, endpoint)
	if err != nil {
		return err
	}
	var ddResponse dataDragonResponse
	if err = json.NewDecoder(response.Body).Decode(&ddResponse); err != nil {
		return err
	}
	// this can not return an error. the error would have been returned during the above decode already
	data, _ := json.Marshal(ddResponse.Data)
	return json.Unmarshal(data, &target)
}

func (c *Client) doRequest(format dataDragonURL, endpoint string) (*http.Response, error) {
	request, err := c.newRequest(format, endpoint)
	if err != nil {
		return nil, err
	}
	response, err := c.client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		var err error
		err, ok := api.StatusToError[response.StatusCode]
		if !ok {
			err = api.Error{
				Message:    "unknown error reason",
				StatusCode: response.StatusCode,
			}
		}
		return nil, err
	}
	return response, nil
}

func (c *Client) newRequest(format dataDragonURL, endpoint string) (*http.Request, error) {
	var version string
	if (strings.Contains(endpoint, "rune") || strings.Contains(endpoint, "mastery")) &&
		versionGreaterThan(c.Version, latestRuneAndMasteryVersion) {
		version = latestRuneAndMasteryVersion
	} else {
		version = c.Version
	}
	var url string
	switch format {
	case dataDragonDataURLFormat:
		url = fmt.Sprintf(string(format), version, c.Language)
	case dataDragonImageURLFormat:
		url = fmt.Sprintf(string(format), version)
	default:
		url = string(format)
	}
	url = "https://" + url + endpoint
	request, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	return request, nil
}

func versionGreaterThan(v1, v2 string) bool {
	v1Split := strings.Split(v1, ".")
	v2Split := strings.Split(v2, ".")
	for i := 0; i < len(v1Split) && i < len(v2Split); i++ {
		int1, err := strconv.Atoi(v1Split[i])
		if err != nil {
			return false
		}
		int2, err := strconv.Atoi(v2Split[i])
		if err != nil {
			return false
		}
		if int1 > int2 {
			return true
		}
	}
	return false
}

type dataDragonResponse struct {
	Type    string
	Format  string
	Version string
	Data    interface{}
}